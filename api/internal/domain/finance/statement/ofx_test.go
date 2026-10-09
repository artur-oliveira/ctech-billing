package statement

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// sgmlHeader is the OFX 1.02 header Brazilian banks still export: it declares
// CHARSET:1252, which real files contradict by being UTF-8. All values in these
// fixtures are invented.
const sgmlHeader = "OFXHEADER:100\r\nDATA:OFXSGML\r\nVERSION:102\r\nSECURITY:NONE\r\nENCODING:USASCII\r\nCHARSET:1252\r\nCOMPRESSION:NONE\r\nOLDFILEUID:NONE\r\nNEWFILEUID:NONE\r\n\r\n"

func sgml(body string) []byte {
	return []byte(sgmlHeader + "<OFX>\r\n<BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL\r\n" +
		"<BANKACCTFROM><BANKID>0000<ACCTID>00000-0</BANKACCTFROM>\r\n<BANKTRANLIST><DTSTART>20260301<DTEND>20260331\r\n" +
		body + "</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>\r\n")
}

// trn writes a transaction with closed leaf tags, as some SGML exporters do.
func trn(typ, date, amount, fitid, name, memo string) string {
	return "<STMTTRN>\r\n<TRNTYPE>" + typ + "</TRNTYPE>\r\n<DTPOSTED>" + date + "</DTPOSTED>\r\n<TRNAMT>" + amount +
		"</TRNAMT>\r\n<FITID>" + fitid + "</FITID>\r\n<NAME>" + name + "</NAME>\r\n<MEMO>" + memo + "</MEMO>\r\n</STMTTRN>\r\n"
}

func TestParseOFXReadsAnSGMLBankStatement(t *testing.T) {
	// Leaf tags open-only (classic SGML) in the first, closed in the second.
	body := "<STMTTRN><TRNTYPE>PAYMENT<DTPOSTED>20260305120000[-3:BRT]<TRNAMT>-150.00<FITID>A1<NAME>Loja<MEMO>Compra no débito</STMTTRN>\r\n" +
		trn("CREDIT", "20260306", "2500.5", "A2", "Fulano", "Pix recebido")
	p, err := ParseOFX(sgml(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{
		{Date: brcal.New(2026, time.March, 5), Amount: -15000, Description: "Compra no débito - Loja", FITID: "A1"},
		{Date: brcal.New(2026, time.March, 6), Amount: 250050, Description: "Pix recebido - Fulano", FITID: "A2"},
	}
	if len(p.Lines) != 2 || p.Lines[0] != want[0] || p.Lines[1] != want[1] || len(p.Rejected) != 0 {
		t.Fatalf("got %+v", p)
	}
}

func TestParseOFXReadsWindows1252(t *testing.T) {
	// "Pagamento café" in Windows-1252: é is 0xE9, not valid UTF-8 on its own.
	raw := sgml(trn("DEBIT", "20260301", "-5,00", "X", "", "Pagamento caf\xe9 \x96 balc\xe3o"))
	p, err := ParseOFX(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Lines[0].Description; got != "Pagamento café – balcão" {
		t.Fatalf("description = %q", got)
	}
	if p.Lines[0].Amount != -500 {
		t.Fatalf("a comma decimal: amount = %d", p.Lines[0].Amount)
	}
}

func TestParseOFXReadsOFX2XML(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?><?OFX OFXHEADER="200" VERSION="220"?>
<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL</CURDEF><BANKTRANLIST>
<STMTTRN><TRNTYPE>DEBIT</TRNTYPE><DTPOSTED>20260310</DTPOSTED><TRNAMT>-42.10</TRNAMT><FITID>X9</FITID><MEMO>Mercado &amp; Cia</MEMO></STMTTRN>
</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`
	p, err := ParseOFX([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lines) != 1 || p.Lines[0].Description != "Mercado & Cia" || p.Lines[0].Amount != -4210 {
		t.Fatalf("got %+v", p.Lines)
	}
}

func TestParseOFXSignConventions(t *testing.T) {
	body := trn("DEBIT", "20260301", "30.00", "1", "", "debit written positive") +
		trn("CREDIT", "20260301", "-30.00", "2", "", "credit written negative") +
		trn("INT", "20260301", "-4.20", "3", "", "overdraft interest keeps its sign") +
		trn("PAYMENT", "20260301", "-9.99", "4", "", "payment")
	p, err := ParseOFX(sgml(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []billing.Cents{-3000, 3000, -420, -999}
	for i, w := range want {
		if p.Lines[i].Amount != w {
			t.Errorf("line %d (%s): %d, want %d", i, p.Lines[i].Description, p.Lines[i].Amount, w)
		}
	}
}

func TestParseOFXRejectsBadLinesAndKeepsTheRest(t *testing.T) {
	body := trn("DEBIT", "20260231", "-1.00", "1", "", "31 February") +
		trn("DEBIT", "20260301", "-1.234", "2", "", "three decimals") +
		trn("DEBIT", "20260301", "0.00", "3", "", "zero") +
		trn("DEBIT", "20260301", "-7.50", "4", "", "fine")
	p, err := ParseOFX(sgml(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lines) != 1 || p.Lines[0].FITID != "4" {
		t.Fatalf("lines = %+v", p.Lines)
	}
	want := []Rejected{{1, ReasonDate}, {2, ReasonAmount}, {3, ReasonZero}}
	if len(p.Rejected) != 3 || p.Rejected[0] != want[0] || p.Rejected[1] != want[1] || p.Rejected[2] != want[2] {
		t.Fatalf("rejected = %+v", p.Rejected)
	}
}

func TestParseOFXRefusesWhatItCannotImport(t *testing.T) {
	card := strings.Replace(string(sgml(trn("DEBIT", "20260301", "-1", "1", "", "x"))), "<BANKMSGSRSV1><STMTTRNRS><STMTRS>",
		"<CREDITCARDMSGSRSV1><CCSTMTTRNRS><CCSTMTRS>", 1)
	two := strings.Replace(string(sgml(trn("DEBIT", "20260301", "-1", "1", "", "x"))), "</STMTRS>", "</STMTRS><STMTRS>", 1)
	usd := strings.Replace(string(sgml(trn("DEBIT", "20260301", "-1", "1", "", "x"))), "<CURDEF>BRL", "<CURDEF>USD", 1)
	big := make([]byte, MaxFileBytes+1)
	for name, c := range map[string]struct {
		raw  []byte
		want error
	}{
		"card":         {[]byte(card), ErrCardStatement},
		"two accounts": {[]byte(two), ErrManyAccounts},
		"dollars":      {[]byte(usd), ErrCurrency},
		"not ofx":      {[]byte("data;valor\n01/03/2026;10,00\n"), ErrUnreadable},
		"empty":        {sgml(""), ErrEmpty},
		"too large":    {big, ErrTooLarge},
	} {
		if _, err := ParseOFX(c.raw); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestParseOFXCapsTheLineCount(t *testing.T) {
	var sb strings.Builder
	for range MaxLines + 1 {
		sb.WriteString(trn("DEBIT", "20260301", "-1.00", "", "", "x"))
	}
	if _, err := ParseOFX(sgml(sb.String())); !errors.Is(err, ErrTooMany) {
		t.Fatalf("%v, want ErrTooMany", err)
	}
}

func TestParseAmount(t *testing.T) {
	for _, c := range []struct {
		in   string
		dec  byte
		want billing.Cents
		ok   bool
	}{
		{"-150.00", 0, -15000, true},
		{"1.234,56", 0, 123456, true},
		{"1,234.56", 0, 123456, true},
		{"1.234.567", 0, 123456700, true},
		{"+10", 0, 1000, true},
		{"10,00-", ',', -1000, true},
		{"(3,50)", ',', -350, true},
		{"R$ 1.000,00", ',', 100000, true},
		{"12.5000", 0, 1250, true},
		{"1.234", ',', 123400, true},
		{"abc", 0, 0, false},
		{"", 0, 0, false},
		{"1,999", '.', 199900, true},
		{"0,001", ',', 0, false},
	} {
		got, ok := parseAmount(c.in, c.dec)
		if got != c.want || ok != c.ok {
			t.Errorf("parseAmount(%q, %q) = %d %v, want %d %v", c.in, c.dec, got, ok, c.want, c.ok)
		}
	}
}

package statement

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// UX batch 5: an OFX bank statement says what the account held (LEDGERBAL), and
// an account that starts with this file can take its opening balance from it.
// All values are invented.

// sgmlWithBalances is sgml() with the balances a bank writes after the list:
// the ledger balance and, often, an available balance that must not be read
// for it.
func sgmlWithBalances(body, balances string) []byte {
	return []byte(sgmlHeader + "<OFX>\r\n<BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL\r\n" +
		"<BANKACCTFROM><BANKID>0000<ACCTID>00000-0</BANKACCTFROM>\r\n<BANKTRANLIST><DTSTART>20260301<DTEND>20260331\r\n" +
		body + "</BANKTRANLIST>\r\n" + balances + "</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>\r\n")
}

const threeLines = "<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260305<TRNAMT>-150.00<FITID>A1<MEMO>Mercado</STMTTRN>\r\n" +
	"<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20260306<TRNAMT>2500.50<FITID>A2<MEMO>Pix recebido</STMTTRN>\r\n" +
	// Out of order on purpose: the first line by date is the third in the file.
	"<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260302120000[-3:BRT]<TRNAMT>100.00<FITID>A3<MEMO>Tarifa</STMTTRN>\r\n"

func TestParseOFXReadsTheLedgerBalanceNotTheAvailableOne(t *testing.T) {
	p, err := ParseOFX(sgmlWithBalances(threeLines,
		"<LEDGERBAL><BALAMT>5000.00<DTASOF>20260331120000[-3:BRT]</LEDGERBAL>\r\n"+
			"<AVAILBAL><BALAMT>9999.99<DTASOF>20260401</AVAILBAL>\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Balance{Amount: 500000, AsOf: brcal.New(2026, time.March, 31)}
	if p.Ledger == nil || *p.Ledger != want {
		t.Fatalf("Ledger = %+v, want %+v", p.Ledger, want)
	}
}

func TestParseOFXWithoutALedgerBalanceHasNone(t *testing.T) {
	p, err := ParseOFX(sgml(threeLines))
	if err != nil {
		t.Fatal(err)
	}
	if p.Ledger != nil {
		t.Fatalf("Ledger = %+v, want none", p.Ledger)
	}
	if _, ok := OpeningFrom(p); ok {
		t.Fatal("an opening balance proposed with no LEDGERBAL")
	}
}

// opening = LEDGERBAL − the sum of every parsed line (credits in, debits out),
// dated the day before the file's first line.
func TestOpeningFromTheLedgerBalance(t *testing.T) {
	cases := []struct {
		name    string
		balance string
		want    Opening
	}{
		// lines: −150.00 −100.00 (DEBIT forces out) +2500.50 = +2250.50
		{"positive", "5000.00", Opening{Amount: 500000 - 225050, Date: brcal.New(2026, time.March, 1), Ledger: Balance{Amount: 500000, AsOf: brcal.New(2026, time.March, 31)}, Lines: 3}},
		{"overdrawn", "-120.00", Opening{Amount: -12000 - 225050, Date: brcal.New(2026, time.March, 1), Ledger: Balance{Amount: -12000, AsOf: brcal.New(2026, time.March, 31)}, Lines: 3}},
		{"exactly the lines", "2250.50", Opening{Amount: 0, Date: brcal.New(2026, time.March, 1), Ledger: Balance{Amount: 225050, AsOf: brcal.New(2026, time.March, 31)}, Lines: 3}},
	}
	for _, c := range cases {
		p, err := ParseOFX(sgmlWithBalances(threeLines, "<LEDGERBAL><BALAMT>"+c.balance+"<DTASOF>20260331</LEDGERBAL>\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := OpeningFrom(p)
		if !ok || got != c.want {
			t.Errorf("%s: OpeningFrom = %+v %v, want %+v", c.name, got, ok, c.want)
		}
	}
}

// A first line on the 1st of a month opens on the last day of the month before.
func TestOpeningIsTheDayBeforeTheFirstLine(t *testing.T) {
	p, err := ParseOFX(sgmlWithBalances(
		"<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20260101<TRNAMT>10.00<FITID>B1<MEMO>x</STMTTRN>\r\n",
		"<LEDGERBAL><BALAMT>10.00<DTASOF>20260131</LEDGERBAL>\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := OpeningFrom(p)
	if !ok || got.Date != brcal.New(2025, time.December, 31) || got.Amount != 0 {
		t.Fatalf("OpeningFrom = %+v %v, want 0 on 31/12/2025", got, ok)
	}
}

// A balance that cannot be read is no balance: nothing is proposed, the lines import.
func TestAnUnreadableLedgerBalanceIsNone(t *testing.T) {
	for _, bal := range []string{"<LEDGERBAL><BALAMT>abc<DTASOF>20260331</LEDGERBAL>", "<LEDGERBAL><BALAMT>10.00<DTASOF>2026</LEDGERBAL>", "<LEDGERBAL><DTASOF>20260331</LEDGERBAL>"} {
		p, err := ParseOFX(sgmlWithBalances(threeLines, bal+"\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		if p.Ledger != nil || len(p.Lines) != 3 {
			t.Errorf("%s: Ledger %+v, lines %d", bal, p.Ledger, len(p.Lines))
		}
	}
}

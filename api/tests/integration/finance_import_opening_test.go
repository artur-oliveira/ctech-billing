//go:build integration

package integration

import (
	"encoding/base64"
	"strings"
	"testing"
)

// UX batch 5 (spec § 3.7): after an OFX import into an account with no opening
// balance and no entries, the statement's LEDGERBAL proposes the opening
// balance: LEDGERBAL − every parsed line, the day before the first line.
// Posting it is explicit, once, inside the space. All values are invented.

// withLedger is a synthetic OFX whose lines are −150.00 (02/02), +2500.50
// (05/02) and −100.00 (03/02, a DEBIT written positive), with LEDGERBAL 5000.00
// on 28/02 and an AVAILBAL that must not be read for it.
func withLedger(balance string) []byte {
	lines := []string{
		ofxLine("L1", "20260202", "-150.00", "Mercado"),
		ofxLine("L2", "20260205", "2500.50", "Pix recebido"),
		"<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260203<TRNAMT>100.00<FITID>L3<MEMO>Tarifa</STMTTRN>",
	}
	raw := "OFXHEADER:100\r\nDATA:OFXSGML\r\nVERSION:102\r\nCHARSET:1252\r\n\r\n<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL" +
		"<BANKACCTFROM><BANKID>0000<ACCTID>00000-0</BANKACCTFROM><BANKTRANLIST>\r\n" + strings.Join(lines, "\r\n") + "\r\n</BANKTRANLIST>"
	if balance != "" {
		raw += "<LEDGERBAL><BALAMT>" + balance + "<DTASOF>20260228</LEDGERBAL><AVAILBAL><BALAMT>9999.99<DTASOF>20260301</AVAILBAL>"
	}
	return []byte(raw + "</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>\r\n")
}

type openingProposal struct {
	Amount        int64  `json:"amount"`
	Date          string `json:"date"`
	LedgerBalance int64  `json:"ledger_balance"`
	LedgerAsOf    string `json:"ledger_as_of"`
	Lines         int    `json:"lines"`
}

type uploadedImport struct {
	ID              string
	OpeningProposal *openingProposal `json:"opening_proposal"`
}

func uploadOFX(t *testing.T, f financeEnv, accountID string, raw []byte) uploadedImport {
	t.Helper()
	var out uploadedImport
	f.must(t, 201, "POST", "/imports", `{"account_id":"`+accountID+`","format":"ofx","content":"`+base64.StdEncoding.EncodeToString(raw)+`"}`, &out)
	return out
}

func newBank(t *testing.T, f financeEnv, name string) string {
	t.Helper()
	var bank struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"`+name+`","class":"asset"}`, &bank)
	return bank.ID
}

func balanceOf(t *testing.T, f financeEnv, accountID string) int64 {
	t.Helper()
	var accounts struct {
		Data []struct {
			ID      string
			Balance int64
		}
	}
	f.must(t, 200, "GET", "/accounts", "", &accounts)
	for _, a := range accounts.Data {
		if a.ID == accountID {
			return a.Balance
		}
	}
	t.Fatalf("account %s not listed", accountID)
	return 0
}

func problemCodeOf(t *testing.T, res apiResponse) string {
	t.Helper()
	var p struct{ Code string }
	res.decode(t, &p)
	return p.Code
}

func TestAnImportOffersTheStatementBalanceAsTheOpeningBalance(t *testing.T) {
	f := newFinanceEnv(t)
	bank := newBank(t, f, "Banco")
	imp := uploadOFX(t, f, bank, withLedger("5000.00"))
	want := openingProposal{Amount: 500000 - (-15000 + 250050 - 10000), Date: "2026-02-01", LedgerBalance: 500000, LedgerAsOf: "2026-02-28", Lines: 3}
	if imp.OpeningProposal == nil || *imp.OpeningProposal != want {
		t.Fatalf("upload proposal = %+v, want %+v", imp.OpeningProposal, want)
	}
	var detail struct {
		Import uploadedImport `json:"import"`
	}
	f.must(t, 200, "GET", "/imports/"+imp.ID, "", &detail)
	if detail.Import.OpeningProposal == nil || *detail.Import.OpeningProposal != want {
		t.Fatalf("detail proposal = %+v", detail.Import.OpeningProposal)
	}

	var posted struct {
		TransactionID string `json:"transaction_id"`
	}
	f.must(t, 201, "POST", "/imports/"+imp.ID+"/opening-balance", `{}`, &posted)
	if posted.TransactionID == "" {
		t.Fatal("no transaction id")
	}
	if got := balanceOf(t, f, bank); got != want.Amount {
		t.Fatalf("balance %d, want the opening %d (no line reconciled yet)", got, want.Amount)
	}
	// Once posted, it is no longer offered (a fresh value: Unmarshal keeps an absent field).
	var after struct {
		Import uploadedImport `json:"import"`
	}
	f.must(t, 200, "GET", "/imports/"+imp.ID, "", &after)
	if after.Import.OpeningProposal != nil {
		t.Fatalf("still offered after posting: %+v", after.Import.OpeningProposal)
	}

	// Idempotent: the same request again (another key) answers the same fact.
	var again struct {
		TransactionID string `json:"transaction_id"`
	}
	f.must(t, 200, "POST", "/imports/"+imp.ID+"/opening-balance", `{}`, &again)
	if again.TransactionID != posted.TransactionID {
		t.Fatalf("second post = %s, want %s", again.TransactionID, posted.TransactionID)
	}
	if got := balanceOf(t, f, bank); got != want.Amount {
		t.Fatalf("balance %d after the repeat, want %d", got, want.Amount)
	}
}

// An overdrawn statement proposes a negative opening.
func TestAnOverdrawnStatementProposesANegativeOpening(t *testing.T) {
	f := newFinanceEnv(t)
	imp := uploadOFX(t, f, newBank(t, f, "Banco"), withLedger("-120.00"))
	if imp.OpeningProposal == nil || imp.OpeningProposal.Amount != -12000-225050 {
		t.Fatalf("proposal = %+v", imp.OpeningProposal)
	}
}

func TestNoOpeningIsOfferedOrPostedWhenTheAccountAlreadyHasOne(t *testing.T) {
	f := newFinanceEnv(t)
	bank := newBank(t, f, "Banco")
	f.must(t, 201, "POST", "/accounts/"+bank+"/opening-balance", `{"amount":1000,"date":"2026-01-01"}`, nil)
	imp := uploadOFX(t, f, bank, withLedger("5000.00"))
	if imp.OpeningProposal != nil {
		t.Fatalf("offered on an account with an opening balance: %+v", imp.OpeningProposal)
	}
	res := f.call(t, "POST", "/imports/"+imp.ID+"/opening-balance", `{}`)
	if res.status != 409 || problemCodeOf(t, res) != "opening_balance_exists" {
		t.Fatalf("post = %d %s, want 409 opening_balance_exists", res.status, res.body)
	}
	if got := balanceOf(t, f, bank); got != 1000 {
		t.Fatalf("balance %d, want the first opening alone", got)
	}
}

func TestNoOpeningIsOfferedOrPostedWhenTheAccountHasEntries(t *testing.T) {
	f := newFinanceEnv(t)
	bank, other := newBank(t, f, "Banco"), newBank(t, f, "Caixa")
	f.must(t, 201, "POST", "/transfers", `{"from_account_id":"`+other+`","to_account_id":"`+bank+`","amount":5000,"date":"2026-01-10"}`, nil)
	imp := uploadOFX(t, f, bank, withLedger("5000.00"))
	if imp.OpeningProposal != nil {
		t.Fatalf("offered on an account with entries: %+v", imp.OpeningProposal)
	}
	res := f.call(t, "POST", "/imports/"+imp.ID+"/opening-balance", `{}`)
	if res.status != 409 || problemCodeOf(t, res) != "account_has_entries" {
		t.Fatalf("post = %d %s, want 409 account_has_entries", res.status, res.body)
	}
	if got := balanceOf(t, f, bank); got != 5000 {
		t.Fatalf("balance %d, want the transfer alone", got)
	}
}

func TestAStatementWithoutALedgerBalanceProposesNothing(t *testing.T) {
	f := newFinanceEnv(t)
	imp := uploadOFX(t, f, newBank(t, f, "Banco"), withLedger(""))
	if imp.OpeningProposal != nil {
		t.Fatalf("proposal = %+v, want none", imp.OpeningProposal)
	}
	res := f.call(t, "POST", "/imports/"+imp.ID+"/opening-balance", `{}`)
	if res.status != 422 || problemCodeOf(t, res) != "no_statement_balance" {
		t.Fatalf("post = %d %s, want 422 no_statement_balance", res.status, res.body)
	}
}

// Only inside the resolved space: another person's space does not know the import.
func TestAnotherSpaceCannotPostAnImportsOpening(t *testing.T) {
	f := newFinanceEnv(t)
	bank := newBank(t, f, "Banco")
	imp := uploadOFX(t, f, bank, withLedger("5000.00"))
	other := otherPersonalSpace(t, f)
	if res := other.call(t, "POST", "/imports/"+imp.ID+"/opening-balance", `{}`); res.status != 404 {
		t.Fatalf("post from another space = %d %s, want 404", res.status, res.body)
	}
	if got := balanceOf(t, f, bank); got != 0 {
		t.Fatalf("balance %d after another space's post", got)
	}
}

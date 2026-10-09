//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// A synthetic OFX 1.02 statement: invented bank, account 00000-0, made-up lines.
func syntheticOFX(lines ...string) []byte {
	return []byte("OFXHEADER:100\r\nDATA:OFXSGML\r\nVERSION:102\r\nCHARSET:1252\r\n\r\n<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL" +
		"<BANKACCTFROM><BANKID>0000<ACCTID>00000-0</BANKACCTFROM><BANKTRANLIST>\r\n" + strings.Join(lines, "\r\n") +
		"\r\n</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>\r\n")
}

func ofxLine(fitid, date, amount, memo string) string {
	return fmt.Sprintf("<STMTTRN><TRNTYPE>OTHER<DTPOSTED>%s<TRNAMT>%s<FITID>%s<MEMO>%s</STMTTRN>", date, amount, fitid, memo)
}

type importsFixture struct {
	billsFixture
	imports *repositories.ImportRepository
}

func newImportsFixture(t *testing.T) importsFixture {
	t.Helper()
	return importsFixture{billsFixture: newBillsFixture(t), imports: repositories.NewImportRepository(testDB, testCfg)}
}

func (f importsFixture) upload(t *testing.T, key string, now time.Time, raw []byte) repositories.Import {
	t.Helper()
	p, err := statement.ParseOFX(raw)
	if err != nil {
		t.Fatal(err)
	}
	imp, err := f.imports.Import(context.Background(), f.sp, "bank", statement.FormatOFX, p, key, now)
	if err != nil {
		t.Fatal(err)
	}
	return imp
}

func (f importsFixture) line(t *testing.T, importID string, n int) repositories.ImportLine {
	t.Helper()
	_, lines, err := f.imports.Get(context.Background(), f.sp, importID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.N == n {
			return l
		}
	}
	t.Fatalf("no line %d", n)
	return repositories.ImportLine{}
}

var threeLines = syntheticOFX(
	ofxLine("T1", "20260309", "-1000.00", "Aluguel março"),
	ofxLine("T2", "20260310", "-5.00", "Café"),
	ofxLine("T3", "20260311", "250.00", "Venda"),
)

func TestTheSameFileTwiceAddsNothing(t *testing.T) {
	f := newImportsFixture(t)
	first := f.upload(t, "k1", time.Now(), threeLines)
	if first.ID == "" || first.Lines != 3 || first.Duplicates != 0 {
		t.Fatalf("first upload = %+v", first)
	}
	second := f.upload(t, "k2", time.Now(), threeLines)
	if second.ID != "" || second.Lines != 0 || second.Duplicates != 3 {
		t.Fatalf("second upload = %+v", second)
	}
	list, err := f.imports.List(context.Background(), f.sp, "bank", time.Now())
	if err != nil || len(list) != 1 {
		t.Fatalf("an upload that adds nothing leaves no row: %d imports, %v", len(list), err)
	}
}

func TestARetriedUploadResumesItsOwnImport(t *testing.T) {
	f := newImportsFixture(t)
	a := f.upload(t, "same-key", time.Now(), threeLines)
	b := f.upload(t, "same-key", time.Now(), threeLines)
	if a.ID != b.ID || b.Lines != 3 || b.Duplicates != 0 {
		t.Fatalf("retry = %+v, first = %+v", b, a)
	}
}

func TestMatchSettlesTheBillWithTheLinesDateAndAmount(t *testing.T) {
	f := newImportsFixture(t)
	bill := f.payable(t, 100000, brcal.New(2026, time.March, 10))
	imp := f.upload(t, "k", time.Now(), threeLines)
	ctx := context.Background()

	_, lines, err := f.imports.Get(ctx, f.sp, imp.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].Status != repositories.LinePending {
		t.Fatalf("line 1 = %+v", lines[0])
	}
	line, paid, err := f.imports.Match(ctx, f.sp, imp.ID, 1, bill.ID, "", repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if line.Status != repositories.LineMatched || paid.Status != finance.BillPaid || paid.PaidDate != brcal.New(2026, time.March, 9) {
		t.Fatalf("line %+v, bill %+v", line, paid)
	}
	if f.bal(t, "bank") != -100000 || f.bal(t, "sys-payables") != 0 {
		t.Fatalf("bank %d, payables %d", f.bal(t, "bank"), f.bal(t, "sys-payables"))
	}
	got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, time.Now())
	if got.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", got.Pending())
	}
}

func TestTwoLinesCannotPayOneBill(t *testing.T) {
	f := newImportsFixture(t)
	bill := f.payable(t, 500, brcal.New(2026, time.March, 10))
	imp := f.upload(t, "k", time.Now(), syntheticOFX(
		ofxLine("A", "20260310", "-5.00", "Café"),
		ofxLine("B", "20260311", "-5.00", "Café"),
	))
	ctx := context.Background()
	if _, _, err := f.imports.Match(ctx, f.sp, imp.ID, 1, bill.ID, "", repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.imports.Match(ctx, f.sp, imp.ID, 2, bill.ID, "", repositories.PostMeta{}, time.Now())
	if !errors.Is(err, finance.ErrBillState) {
		t.Fatalf("second match: %v, want ErrBillState", err)
	}
	if l := f.line(t, imp.ID, 2); l.Status != repositories.LinePending {
		t.Fatalf("the losing line stays pending: %+v", l)
	}
	if f.bal(t, "bank") != -500 {
		t.Fatalf("bank = %d: paid twice", f.bal(t, "bank"))
	}
}

func TestANewLineIsRecognisedAndSettledInOneWrite(t *testing.T) {
	f := newImportsFixture(t)
	imp := f.upload(t, "k", time.Now(), threeLines)
	ctx := context.Background()
	line, bill, err := f.imports.Create(ctx, f.sp, imp.ID, 2, "food", "", repositories.PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if line.Status != repositories.LineCreated || bill.Status != finance.BillPaid || bill.Origin != finance.OriginImport ||
		len(bill.TransactionIDs) != 2 || bill.Description != "Café" {
		t.Fatalf("line %+v, bill %+v", line, bill)
	}
	if f.bal(t, "food") != 500 || f.bal(t, "bank") != -500 || f.bal(t, "sys-payables") != 0 {
		t.Fatalf("food %d bank %d payables %d", f.bal(t, "food"), f.bal(t, "bank"), f.bal(t, "sys-payables"))
	}
	stored, err := f.bills.Get(ctx, f.sp, bill.ID)
	if err != nil || stored.Status != finance.BillPaid {
		t.Fatalf("stored bill = %+v, %v", stored, err)
	}
	if _, _, err := f.imports.Create(ctx, f.sp, imp.ID, 2, "food", "", repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrLineResolved) {
		t.Fatalf("a second create: %v, want ErrLineResolved", err)
	}
	// Money in becomes a receivable under an income category.
	_, in, err := f.imports.Create(ctx, f.sp, imp.ID, 3, "sales", "Venda balcão", repositories.PostMeta{}, time.Now())
	if err != nil || in.Direction != finance.Receivable || in.Description != "Venda balcão" || f.bal(t, "bank") != 24500 {
		t.Fatalf("receivable = %+v, %v, bank %d", in, err, f.bal(t, "bank"))
	}
	// A payable line under an income category is refused.
	if _, _, err := f.imports.Create(ctx, f.sp, imp.ID, 1, "sales", "", repositories.PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidBill) {
		t.Fatalf("wrong class: %v", err)
	}
}

func TestIgnoreAndReopen(t *testing.T) {
	f := newImportsFixture(t)
	imp := f.upload(t, "k", time.Now(), threeLines)
	ctx := context.Background()
	if _, err := f.imports.Ignore(ctx, f.sp, imp.ID, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.imports.Ignore(ctx, f.sp, imp.ID, 2, time.Now()); !errors.Is(err, repositories.ErrLineResolved) {
		t.Fatalf("ignoring twice: %v", err)
	}
	if _, _, err := f.imports.Create(ctx, f.sp, imp.ID, 2, "food", "", repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrLineResolved) {
		t.Fatalf("an ignored line is not offered again: %v", err)
	}
	if got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, time.Now()); got.Pending() != 2 {
		t.Fatalf("pending = %d", got.Pending())
	}
	if _, err := f.imports.Reopen(ctx, f.sp, imp.ID, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if l := f.line(t, imp.ID, 2); l.Status != repositories.LinePending {
		t.Fatalf("reopened line = %+v", l)
	}
	if got, _, _ := f.imports.Get(ctx, f.sp, imp.ID, time.Now()); got.Pending() != 3 {
		t.Fatalf("pending after reopen = %d", got.Pending())
	}
	if _, err := f.imports.Reopen(ctx, f.sp, imp.ID, 1, time.Now()); !errors.Is(err, repositories.ErrLineResolved) {
		t.Fatalf("only an ignored line reopens: %v", err)
	}
	// Ignored, the transaction still never imports again.
	if _, err := f.imports.Ignore(ctx, f.sp, imp.ID, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if again := f.upload(t, "k2", time.Now(), threeLines); again.Duplicates != 3 {
		t.Fatalf("after ignore, re-upload = %+v", again)
	}
}

func TestAMatchMustBeTheLinesAccountAndDirection(t *testing.T) {
	f := newImportsFixture(t)
	ctx, now := context.Background(), time.Now()
	if err := f.ledger.CreateAccount(ctx, f.sp, finance.LedgerAccount{ID: "savings", Name: "Poupança", Class: finance.ClassAsset}, now); err != nil {
		t.Fatal(err)
	}
	other, err := f.bills.Create(ctx, f.sp, finance.Bill{
		Direction: finance.Payable, Amount: 100000, AccountID: "savings", CategoryID: "rent",
		Competence: brcal.New(2026, time.March, 1), Due: brcal.New(2026, time.March, 10), Origin: finance.OriginManual,
	}, repositories.PostMeta{}, now)
	if err != nil {
		t.Fatal(err)
	}
	imp := f.upload(t, "k", now, threeLines)
	if _, _, err := f.imports.Match(ctx, f.sp, imp.ID, 1, other.ID, "", repositories.PostMeta{}, now); !errors.Is(err, repositories.ErrLineMismatch) {
		t.Fatalf("another account's bill: %v", err)
	}
	if f.bal(t, "savings") != 0 {
		t.Fatal("a refused match moved cash")
	}
}

func TestAnImportFromAnotherSpaceIsNotFound(t *testing.T) {
	f := newImportsFixture(t)
	imp := f.upload(t, "k", time.Now(), threeLines)
	stranger := jobSpace(t, newSpaceOrgID(), true)
	if _, _, err := f.imports.Get(context.Background(), stranger, imp.ID, time.Now()); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("Get from another space: %v", err)
	}
	if _, err := f.imports.Ignore(context.Background(), stranger, imp.ID, 1, time.Now()); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("Ignore from another space: %v", err)
	}
}

func TestOnlyACashAccountTakesAnImport(t *testing.T) {
	f := newImportsFixture(t)
	p, _ := statement.ParseOFX(threeLines)
	if _, err := f.imports.Import(context.Background(), f.sp, "rent", statement.FormatOFX, p, "", time.Now()); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("a category as the account: %v", err)
	}
}

// A line nobody reconciled for 90 days expires with its import; its lock does
// not, but it no longer blocks the transaction from being imported again.
func TestAnExpiredUnreconciledLineCanBeImportedAgain(t *testing.T) {
	f := newImportsFixture(t)
	long := time.Now().AddDate(0, 0, -100)
	old := f.upload(t, "old", long, syntheticOFX(ofxLine("A", "20260101", "-5.00", "Café"), ofxLine("B", "20260102", "-7.00", "Pão")))
	if _, err := f.imports.Ignore(context.Background(), f.sp, old.ID, 2, long); err != nil {
		t.Fatal(err)
	}
	again := f.upload(t, "new", time.Now(), syntheticOFX(ofxLine("A", "20260101", "-5.00", "Café"), ofxLine("B", "20260102", "-7.00", "Pão")))
	if again.Lines != 1 || again.Duplicates != 1 {
		t.Fatalf("the unreconciled line returns, the ignored one does not: %+v", again)
	}
}

func TestTheCSVMappingIsKeptPerAccount(t *testing.T) {
	f := newImportsFixture(t)
	ctx := context.Background()
	if _, err := f.imports.GetMapping(ctx, f.sp, "bank"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("no mapping yet: %v", err)
	}
	m := statement.Mapping{Delimiter: ";", Decimal: ",", DateFormat: statement.DateDMY, SkipRows: 1, Date: 1, Description: 2, Amount: 3}
	if err := f.imports.PutMapping(ctx, f.sp, "bank", m, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := f.imports.GetMapping(ctx, f.sp, "bank")
	if err != nil || got != m {
		t.Fatalf("mapping = %+v, %v", got, err)
	}
	if err := f.imports.PutMapping(ctx, f.sp, "bank", statement.Mapping{}, time.Now()); !errors.Is(err, statement.ErrInvalidMapping) {
		t.Fatalf("an invalid mapping: %v", err)
	}
	other := jobSpace(t, newSpaceOrgID(), true)
	if _, err := f.imports.GetMapping(ctx, other, "bank"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("another space's mapping: %v", err)
	}
}

//go:build integration

package integration

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The three writes an invoice has on the console surface (C3): finalize, void,
// and the credit note. What is tested here is what the domain cannot enforce on
// its own — who the trail says did it, what happens on the second click, and the
// two ways an operator can be told "no".

func (e *apiEnv) consolePost(t *testing.T, path, token, mode, body string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(middleware.ModeHeader, mode)

	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: payload}
}

type consoleInvoiceDetail struct {
	Invoice struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Number int64  `json:"number"`
		Total  int64  `json:"total"`
	} `json:"invoice"`
	CreditNotes []struct {
		ID        string `json:"id"`
		Amount    int64  `json:"amount"`
		Reason    string `json:"reason"`
		CreatedBy string `json:"created_by"`
	} `json:"credit_notes"`
	Credited      int64 `json:"credited"`
	FullyCredited bool  `json:"fully_credited"`
	Timeline      []struct {
		Action string `json:"action"`
		Actor  string `json:"actor"`
		Cause  string `json:"cause"`
	} `json:"timeline"`
}

// Finalizing from the console must produce the invoice the sweep would have —
// numbered, OPEN, dunning armed — and a trail that names the operator, not the
// scheduler. The cause used to be hard-coded; this is the test that keeps it
// threaded.
func TestConsoleFinalizeIssuesTheDraftAsThePersonWhoClicked(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead)
	inv := newDraftInvoice(t, e.org, "gen_"+id.New())

	res := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/finalize", token, "live", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body consoleInvoiceDetail
	res.decode(t, &body)

	if body.Invoice.Status != string(billing.InvoiceOpen) {
		t.Fatalf("status = %q, want OPEN", body.Invoice.Status)
	}
	if body.Invoice.Number == 0 {
		t.Fatal("a finalized invoice carries a number")
	}
	var finalized bool
	for _, entry := range body.Timeline {
		if entry.Action != string(billing.EventInvoiceFinalized) {
			continue
		}
		finalized = true
		if entry.Cause != string(billing.CauseManual) {
			t.Errorf("cause = %q, want manual — the scheduler did not do this", entry.Cause)
		}
		if len(entry.Actor) < 5 || entry.Actor[:5] != "user:" {
			t.Errorf("actor = %q, want the signed-in operator", entry.Actor)
		}
	}
	if !finalized {
		t.Fatal("the timeline must carry the finalization")
	}
}

// A double click must not burn a second invoice number. Numbering is gapless
// per organization and per year, so a second number spent on the same document
// is a gap that cannot be repaired afterwards.
func TestConsoleFinalizeTwiceKeepsOneNumber(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead)
	inv := newDraftInvoice(t, e.org, "gen_"+id.New())

	first := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/finalize", token, "live", "")
	second := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/finalize", token, "live", "")
	if first.status != http.StatusOK || second.status != http.StatusOK {
		t.Fatalf("statuses = %d, %d: %s", first.status, second.status, second.body)
	}
	var a, b consoleInvoiceDetail
	first.decode(t, &a)
	second.decode(t, &b)
	if a.Invoice.Number != b.Invoice.Number {
		t.Fatalf("numbers = %d and %d; the second click must not spend a number", a.Invoice.Number, b.Invoice.Number)
	}
}

func TestConsoleVoidsAnOpenInvoice(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead)

	inv := newDraftInvoice(t, e.org, "gen_"+id.New())
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	due := brcal.New(2026, time.March, 10)
	if _, err := invoices.Finalize(ctx, inv, due, due, billing.CauseScheduler, "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	res := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/void", token, "live", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body consoleInvoiceDetail
	res.decode(t, &body)
	if body.Invoice.Status != string(billing.InvoiceVoid) {
		t.Fatalf("status = %q, want VOID", body.Invoice.Status)
	}

	// Repeating it is a double click, not an error, and writes no second row.
	again := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/void", token, "live", "")
	if again.status != http.StatusOK {
		t.Fatalf("second void status = %d: %s", again.status, again.body)
	}
	var second consoleInvoiceDetail
	again.decode(t, &second)
	voids := 0
	for _, entry := range second.Timeline {
		if entry.Action == string(billing.EventInvoiceVoided) {
			voids++
		}
	}
	if voids != 1 {
		t.Fatalf("voided entries = %d, want exactly one", voids)
	}
}

// The whole point of the credit note: an issued invoice is corrected by a new
// document, never by editing the old one. The detail carries the note, the
// credited total, and the "fully credited" answer the screen renders as
// "estornada" — which is deliberately not a status.
func TestConsoleIssuesACreditNoteAgainstAnOpenInvoice(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead)

	inv := newDraftInvoice(t, e.org, "gen_"+id.New())
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	due := brcal.New(2026, time.March, 10)
	if _, err := invoices.Finalize(ctx, inv, due, due, billing.CauseScheduler, "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	body := `{"amount":1990,"reason":"cobrança em duplicidade"}`
	res := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/credit-notes", token, "live", body)
	if res.status != http.StatusCreated {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}

	detail := e.console(t, "/v1.0/console/invoices/"+inv.ID, token, "live")
	var view consoleInvoiceDetail
	detail.decode(t, &view)

	if len(view.CreditNotes) != 1 {
		t.Fatalf("credit notes = %d, want 1: %s", len(view.CreditNotes), detail.body)
	}
	if view.Credited != 1990 {
		t.Errorf("credited = %d, want 1990", view.Credited)
	}
	if view.FullyCredited {
		t.Error("a partial credit is not a full one")
	}
	if view.CreditNotes[0].CreatedBy[:5] != "user:" {
		t.Errorf("created_by = %q, want the signed-in operator", view.CreditNotes[0].CreatedBy)
	}
	// The invoice itself did not move: the money owed and the money credited are
	// two facts, and collapsing them destroys the first.
	if view.Invoice.Status != string(billing.InvoiceOpen) {
		t.Errorf("status = %q, want the invoice untouched", view.Invoice.Status)
	}
}

// Over-crediting turns "you owe us X" into "we owe you Y" with nobody deciding
// to. The second note is refused against the total the first one already used.
func TestConsoleRefusesToCreditMoreThanTheInvoiceTotal(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead)

	inv := newDraftInvoice(t, e.org, "gen_"+id.New())
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	due := brcal.New(2026, time.March, 10)
	if _, err := invoices.Finalize(ctx, inv, due, due, billing.CauseScheduler, "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	full := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/credit-notes", token, "live",
		`{"amount":4990,"reason":"serviço não prestado"}`)
	if full.status != http.StatusCreated {
		t.Fatalf("first credit status = %d: %s", full.status, full.body)
	}
	over := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/credit-notes", token, "live",
		`{"amount":100,"reason":"mais um"}`)
	if over.status < 400 || over.status >= 500 {
		t.Fatalf("status = %d, want a refusal: %s", over.status, over.body)
	}

	detail := e.console(t, "/v1.0/console/invoices/"+inv.ID, token, "live")
	var view consoleInvoiceDetail
	detail.decode(t, &view)
	if view.Credited != 4990 {
		t.Fatalf("credited = %d, want the refused note to have written nothing", view.Credited)
	}
	if !view.FullyCredited {
		t.Error("crediting the whole total is fully credited")
	}
}

// A DRAFT has not been issued, so there is nothing to correct — the lines are
// still editable. This is the refusal that keeps a credit note meaning what it
// says.
func TestConsoleRefusesToCreditADraft(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite)
	inv := newDraftInvoice(t, e.org, "gen_"+id.New())

	res := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/credit-notes", token, "live",
		`{"amount":100,"reason":"cedo demais"}`)
	if res.status < 400 || res.status >= 500 {
		t.Fatalf("status = %d, want a refusal: %s", res.status, res.body)
	}
}

// A credit note with no reason is the document nobody can explain a year later.
func TestConsoleRefusesACreditNoteWithNoReason(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite)
	inv := newDraftInvoice(t, e.org, "gen_"+id.New())

	res := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/credit-notes", token, "live",
		`{"amount":100}`)
	if res.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.status, res.body)
	}
}

// The read scope renders the screen; it must not be able to act from it.
func TestConsoleWritesNeedTheWriteScope(t *testing.T) {
	e := newAPI(t)
	readOnly := e.sessionToken(t, middleware.ScopeInvoicesRead)
	inv := newDraftInvoice(t, e.org, "gen_"+id.New())

	for _, path := range []string{"/finalize", "/void", "/credit-notes"} {
		res := e.consolePost(t, "/v1.0/console/invoices/"+inv.ID+path, readOnly, "live",
			`{"amount":100,"reason":"x"}`)
		if res.status != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403: %s", path, res.status, res.body)
		}
	}
}

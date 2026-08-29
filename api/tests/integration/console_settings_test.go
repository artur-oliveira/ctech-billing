//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// C17, C1, and the two things they made possible: a configurable dunning
// schedule and an audited tax-id reveal.

func TestConsoleSettingsPublishTheScheduleInForce(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeOrganizationRead)

	res := e.console(t, "/v1.0/console/settings", token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Dunning struct {
			Steps  []struct{ Offset int } `json:"steps"`
			Custom bool                   `json:"custom"`
		} `json:"dunning"`
	}
	res.decode(t, &body)

	// An organization that configured nothing still follows a policy, and the
	// screen's question is "what happens to an unpaid invoice" — to which
	// "nothing configured" is not an answer.
	if len(body.Dunning.Steps) != len(billing.DefaultDunningPolicy) {
		t.Fatalf("steps = %d, want the built-in %d", len(body.Dunning.Steps), len(billing.DefaultDunningPolicy))
	}
	if body.Dunning.Custom {
		t.Error("an inherited policy must not claim to be custom")
	}
}

func TestConsoleChangesTheDunningPolicy(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeOrganizationRead)

	res := e.consolePut(t, "/v1.0/console/settings/dunning", token, "live",
		`{"steps":[{"offset":-1,"action":"remind"},{"offset":5,"action":"escalate"},{"offset":20,"action":"abandon"}]}`)
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}

	after := e.console(t, "/v1.0/console/settings", token, "live")
	var body struct {
		Dunning struct {
			Steps  []struct{ Offset int } `json:"steps"`
			Custom bool                   `json:"custom"`
		} `json:"dunning"`
	}
	after.decode(t, &body)
	if !body.Dunning.Custom || len(body.Dunning.Steps) != 3 {
		t.Fatalf("policy = %+v, want the three steps just written", body.Dunning)
	}

	// Empty restores the default rather than disabling dunning: an invoice that
	// is never chased and never written off sits OPEN forever looking like
	// revenue.
	if reset := e.consolePut(t, "/v1.0/console/settings/dunning", token, "live", `{"steps":[]}`); reset.status != http.StatusOK {
		t.Fatalf("reset status = %d: %s", reset.status, reset.body)
	}
	restored := e.console(t, "/v1.0/console/settings", token, "live")
	restored.decode(t, &body)
	if body.Dunning.Custom || len(body.Dunning.Steps) != len(billing.DefaultDunningPolicy) {
		t.Fatalf("after reset = %+v, want the built-in policy", body.Dunning)
	}
}

// Every refusal here is a way of hurting a customer that is invisible in the
// stored data afterwards.
func TestConsoleRefusesAnUnfollowablePolicy(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite)

	for _, body := range []string{
		`{"steps":[{"offset":5,"action":"remind"},{"offset":1,"action":"remind"}]}`,
		`{"steps":[{"offset":-3,"action":"escalate"}]}`,
		`{"steps":[{"offset":1,"action":"abandon"},{"offset":10,"action":"remind"}]}`,
	} {
		res := e.consolePut(t, "/v1.0/console/settings/dunning", token, "live", body)
		if res.status < 400 || res.status >= 500 {
			t.Errorf("status = %d for %s, want a refusal", res.status, body)
		}
	}
}

// The invoice carries its own copy of the schedule, so changing the policy does
// not move the write-off date of a bill already being chased. This is the
// property the whole copy-on-finalize design exists for.
func TestChangingThePolicyDoesNotRewriteAnInvoiceInFlight(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite)

	// Written with its policy already on it, because that is when a real invoice
	// gets one: GenerateForPeriod resolves the schedule and stamps it before the
	// row exists. Finalized with **no** settlement date so this invoice never
	// enters the shared dunning queue — the queue is cross-tenant, and an
	// invoice armed here would be chased by whichever dunning test runs on that
	// day.
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	inv := &billing.Invoice{
		ID:             id.NewWithPrefix(id.PrefixInvoice),
		OrganizationID: e.org.ID,
		Livemode:       e.org.Livemode,
		CustomerID:     "cus_policy",
		Status:         billing.InvoiceDraft,
		Period:         marchPeriod(),
		Currency:       billing.CurrencyBRL,
		Total:          4990,
		Subtotal:       4990,
		Policy: billing.DunningSchedule{
			{Offset: -3, Action: billing.DunningRemind},
			{Offset: 30, Action: billing.DunningAbandon},
		},
	}
	items := []billing.InvoiceItem{{Description: "Plano", Period: marchPeriod(), Quantity: 1, UnitAmount: 4990, Amount: 4990}}
	if err := invoices.Create(ctx, inv, items, "gen_"+id.New(), now()); err != nil {
		t.Fatal(err)
	}
	due := brcal.New(2026, time.March, 10)
	if _, err := invoices.Finalize(ctx, inv, due, brcal.Date{}, billing.CauseScheduler, "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	res := e.consolePut(t, "/v1.0/console/settings/dunning", token, "live",
		`{"steps":[{"offset":1,"action":"remind"}]}`)
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}

	stored, err := invoices.Get(ctx, e.org.ID, e.org.Livemode, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Policy) != 2 || stored.Policy[1].Action != billing.DunningAbandon {
		t.Fatalf("policy = %+v, want the two steps it was issued under", stored.Policy)
	}
}

// Revealing a CPF is audited, or the masking everywhere else is theatre: a
// data-subject request asking "who has seen my tax id" would have no honest
// answer.
func TestRevealingATaxIDWritesTheAuditRow(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeCustomersWrite, middleware.ScopeCustomersRead)

	customer := &billing.Customer{
		ID:             id.NewWithPrefix(id.PrefixCustomer),
		OrganizationID: e.org.ID,
		Livemode:       e.org.Livemode,
		Name:           "Padaria do Bairro",
		TaxID:          "12345678909",
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).
		Create(ctx, customer, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	res := e.consolePost(t, "/v1.0/console/customers/"+customer.ID+"/tax-id", token, "live", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var revealed struct {
		TaxID string `json:"tax_id"`
	}
	res.decode(t, &revealed)
	if revealed.TaxID != "12345678909" {
		t.Fatalf("tax_id = %q, want the full value", revealed.TaxID)
	}

	detail := e.console(t, "/v1.0/console/customers/"+customer.ID, token, "live")
	var view struct {
		Customer struct {
			TaxIDMasked string `json:"tax_id_masked"`
		} `json:"customer"`
		Timeline []struct {
			Action string `json:"action"`
			Actor  string `json:"actor"`
		} `json:"timeline"`
	}
	detail.decode(t, &view)

	// The detail still masks it. Revealing is an act somebody performs, not a
	// field that starts appearing once they have performed it.
	if view.Customer.TaxIDMasked == "12345678909" {
		t.Error("the detail must keep masking the tax id")
	}
	var logged bool
	for _, entry := range view.Timeline {
		if entry.Action == billing.AuditTaxIDRevealed {
			logged = true
			if len(entry.Actor) < 5 || entry.Actor[:5] != "user:" {
				t.Errorf("actor = %q, want the operator who looked", entry.Actor)
			}
		}
	}
	if !logged {
		t.Fatal("revealing a tax id must leave a record of who looked")
	}
}

// Reading a customer is not being trusted with their CPF.
func TestRevealingATaxIDNeedsTheWriteScope(t *testing.T) {
	e := newAPI(t)
	readOnly := e.sessionToken(t, middleware.ScopeCustomersRead)

	res := e.consolePost(t, "/v1.0/console/customers/cus_whatever/tax-id", readOnly, "live", "")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", res.status, res.body)
	}
}

// C1 counts one month and says so. An overview that presented a partial sum as
// a total would be a number a merchant plans around and cannot trust.
func TestOverviewSeparatesReceivedOpenAndOverdue(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesRead)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)

	// Paid.
	paid := newDraftInvoice(t, e.org, "gen_"+id.New())
	due := brcal.New(2026, time.March, 10)
	if _, err := invoices.Finalize(ctx, paid, due, due, billing.CauseScheduler, "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := invoices.Transition(ctx, paid, billing.InvoicePaid, billing.CauseManualPayment, "operator", "req_2", now()); err != nil {
		t.Fatal(err)
	}
	// Open and long past due.
	overdue := newDraftInvoice(t, e.org, "gen_"+id.New())
	if _, err := invoices.Finalize(ctx, overdue, brcal.New(2020, time.January, 10), due, billing.CauseScheduler, "scheduler", "req_3", now()); err != nil {
		t.Fatal(err)
	}
	// Never finalized.
	newDraftInvoice(t, e.org, "gen_"+id.New())

	today := brcal.FromTime(now())
	res := e.console(t, "/v1.0/console/overview?year="+itoa(today.Year)+"&month="+itoa(int(today.Month)), token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Received     int64 `json:"received"`
		Open         int64 `json:"open"`
		Overdue      int64 `json:"overdue"`
		Drafts       int   `json:"drafts"`
		OverdueCount int   `json:"overdue_count"`
		Complete     bool  `json:"complete"`
	}
	res.decode(t, &body)

	if body.Received == 0 {
		t.Error("a paid invoice belongs in received")
	}
	if body.Overdue == 0 || body.OverdueCount == 0 {
		t.Error("an invoice past its due date belongs in overdue, not open")
	}
	if body.Drafts == 0 {
		t.Error("a draft nothing will ever pick up is the one count worth surfacing")
	}
	if !body.Complete {
		t.Error("a month with three invoices fits one page")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// The issuer block is what the PDF is headed by, and it is written as one unit:
// a partial update is how a document ends up carrying one company's name over
// another's CNPJ.
func TestConsoleSetsTheIssuerAsABlock(t *testing.T) {
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeOrganizationRead)

	res := e.consolePut(t, "/v1.0/console/settings/issuer", token, "live",
		`{"legal_name":"A O CARVALHO TECH LTDA","tax_id":"12.345.678/0001-90","address":"São Paulo/SP","email":"cobranca@aoctech.app"}`)
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}

	saved := readIssuer(t, e, token)
	if saved.LegalName != "A O CARVALHO TECH LTDA" || saved.TaxID == "" {
		t.Fatalf("issuer = %+v, want what was just written", saved)
	}

	// An empty field clears rather than being ignored: an organization that
	// removed its address must not keep printing it.
	if cleared := e.consolePut(t, "/v1.0/console/settings/issuer", token, "live",
		`{"legal_name":"A O CARVALHO TECH LTDA","tax_id":"","address":"","email":""}`); cleared.status != http.StatusOK {
		t.Fatalf("clear status = %d: %s", cleared.status, cleared.body)
	}
	// A **fresh** struct, and that is not a detail: the cleared fields are
	// `omitempty`, so they are absent from the second response, and decoding
	// into the struct the first read filled would leave the old values standing
	// and pass a test that proves nothing.
	after := readIssuer(t, e, token)
	if after.TaxID != "" || after.Address != "" {
		t.Fatalf("issuer = %+v, want the cleared fields gone", after)
	}
}

type issuerView struct {
	LegalName string `json:"legal_name"`
	TaxID     string `json:"tax_id"`
	Address   string `json:"address"`
	Email     string `json:"email"`
}

func readIssuer(t *testing.T, e *apiEnv, token string) issuerView {
	t.Helper()
	res := e.console(t, "/v1.0/console/settings", token, "live")
	if res.status != http.StatusOK {
		t.Fatalf("settings status = %d: %s", res.status, res.body)
	}
	var body struct {
		Issuer issuerView `json:"issuer"`
	}
	res.decode(t, &body)
	return body.Issuer
}

// Changing the issuer leaves a trail, because it changes what every future
// document says about who charged somebody.
func TestChangingTheIssuerIsAudited(t *testing.T) {
	ctx := ctxT(t)
	e := newAPI(t)
	token := e.sessionToken(t, middleware.ScopeInvoicesWrite)

	if res := e.consolePut(t, "/v1.0/console/settings/issuer", token, "live",
		`{"legal_name":"NOVA RAZAO LTDA","tax_id":"","address":"","email":""}`); res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}

	audit := repositories.NewAuditRepository(testDB, testCfg)
	entries, err := audit.ListForEntity(ctx, e.org.ID, e.org.Livemode, e.org.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Action == "organization.issuer_changed" {
			found = true
			if entry.After != "NOVA RAZAO LTDA" {
				t.Errorf("after = %q, want the new legal name", entry.After)
			}
		}
	}
	if !found {
		t.Fatal("changing the issuer must leave an audit row")
	}
}

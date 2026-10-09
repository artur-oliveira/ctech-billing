package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// financeHandlers serves /v1.0/console/finance. Every handler gets its space
// from middleware.GetSpace — resolved by the server, never from the request — and
// hands it to a repository that refuses anything else.
type financeHandlers struct {
	bills   *services.FinanceBills
	imports *services.FinanceImports
	jobs    *services.FinanceJobs
	recs    *repositories.RecurrenceRepository
	cards   *repositories.CardRepository
	ledger  *repositories.LedgerRepository
	clock   func() time.Time
	spaces  spaceLister
	// ensured runs a space's creation once per process, however many requests
	// arrive together.
	ensured ensureOnce
}

// ensureOnce runs a function once per key and remembers only success. Callers
// for the same key wait for the one in flight instead of repeating its writes:
// the app opens a space with several parallel requests, and each running the
// creation transaction is what made them collide.
type ensureOnce struct {
	done  sync.Map // key -> struct{}
	locks sync.Map // key -> *sync.Mutex
}

func (e *ensureOnce) do(key string, fn func() error) error {
	if _, ok := e.done.Load(key); ok {
		return nil
	}
	m, _ := e.locks.LoadOrStore(key, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if _, ok := e.done.Load(key); ok {
		return nil
	}
	if err := fn(); err != nil {
		return err
	}
	e.done.Store(key, struct{}{})
	return nil
}

func (h *financeHandlers) now() time.Time    { return h.clock() }
func (h *financeHandlers) today() brcal.Date { return brcal.FromTime(h.clock()) }

// financeRoute is one row of the route table. The registration and the verb-gate
// test both read this table, so a route cannot exist without a verb, a scope and
// a stated idempotency rule.
type financeRoute struct {
	Method, Path string
	Verb         space.Verbs
	// Write means the route changes data: it needs the write scope and an
	// Idempotency-Key. Reads (including the stateless preview) need neither.
	Write   bool
	Handler func(*financeHandlers) fiber.Handler
}

func financeRoutes() []financeRoute {
	return []financeRoute{
		{"GET", "/space", space.Read, false, func(*financeHandlers) fiber.Handler { return financeSpace }},

		{"GET", "/bills", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.listBills }},
		{"POST", "/bills", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.createBill }},
		{"GET", "/bills/:id", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.getBill }},
		{"PATCH", "/bills/:id", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.patchBill }},
		{"POST", "/bills/:id/settle", space.Settle, true, func(h *financeHandlers) fiber.Handler { return h.settleBill }},
		{"POST", "/bills/:id/cancel", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.cancelBill }},
		{"POST", "/bills/:id/unsettle", space.Settle, true, func(h *financeHandlers) fiber.Handler { return h.unsettleBill }},

		{"GET", "/recurrences", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.listRecurrences }},
		{"POST", "/recurrences", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.createRecurrence }},
		{"POST", "/recurrences/preview", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.previewRecurrence }},
		{"GET", "/recurrences/:id/occurrences", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.recurrenceOccurrences }},
		{"PATCH", "/recurrences/:id", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.patchRecurrence }},
		{"POST", "/recurrences/:id/archive", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.archiveRecurrence }},

		{"GET", "/projection", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.projection }},

		{"GET", "/accounts", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.listAccounts }},
		{"POST", "/accounts", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.createAccount }},
		{"POST", "/accounts/:id/archive", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.archiveAccount }},
		{"GET", "/accounts/:id/statement", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.statement }},
		{"POST", "/accounts/:id/opening-balance", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.openingBalance }},
		{"POST", "/transfers", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.transfer }},
		{"POST", "/transactions/:id/reverse", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.reverse }},
		// cards
		{"GET", "/cards", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.listCards }},
		{"POST", "/cards", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.createCard }},
		{"PATCH", "/cards/:id", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.patchCard }},
		{"GET", "/cards/:id/statements/:month", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.cardStatement }},
		{"GET", "/cards/:id/purchases", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.listPurchases }},
		{"POST", "/cards/:id/purchases", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.createPurchase }},
		{"POST", "/cards/:id/purchases/:pid/refund", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.refundPurchase }},
		{"POST", "/cards/:id/purchases/:pid/advance", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.advancePurchase }},
		{"POST", "/cards/:id/close", space.Write, true, func(h *financeHandlers) fiber.Handler { return h.closeStatement }},
		// imports (F6)
		{"GET", "/imports", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.listImports }},
		{"POST", "/imports", space.Import, true, func(h *financeHandlers) fiber.Handler { return h.uploadImport }},
		{"GET", "/imports/:id", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.getImport }},
		{"POST", "/imports/:id/lines/:n/match", space.Import | space.Write | space.Settle, true, func(h *financeHandlers) fiber.Handler { return h.matchLine }},
		{"POST", "/imports/:id/lines/:n/new", space.Import | space.Write | space.Settle, true, func(h *financeHandlers) fiber.Handler { return h.newFromLine }},
		// Linking posts nothing (the job already paid the bill), but it binds a bill.
		{"POST", "/imports/:id/lines/:n/link", space.Import | space.Write, true, func(h *financeHandlers) fiber.Handler { return h.linkLine }},
		{"POST", "/imports/:id/lines/:n/ignore", space.Import, true, func(h *financeHandlers) fiber.Handler { return h.ignoreLine }},
		{"POST", "/imports/:id/lines/:n/reopen", space.Import, true, func(h *financeHandlers) fiber.Handler { return h.reopenLine }},
		{"GET", "/accounts/:id/csv-mapping", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.getCSVMapping }},
		{"PUT", "/accounts/:id/csv-mapping", space.Import, true, func(h *financeHandlers) fiber.Handler { return h.putCSVMapping }},
		{"GET", "/reports/dre", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.dre }},
		{"GET", "/reports/cash-flow", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.cashFlow }},
		{"GET", "/settings", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.getSettings }},
		{"PUT", "/settings/default-receiving-account", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.setDefaultReceivingAccount }},
		{"PUT", "/settings/post-ctech-invoices", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.setPostCTechInvoices }},
	}
}

// financeChain is the middleware every finance route runs, in this order:
// scope, then the space (404/503 before anything is read), then the verb, then —
// for writes only — the space-scoped idempotency key.
func financeChain(r financeRoute, resolver *space.Resolver, idem fiber.Handler) []any {
	scope := middleware.ScopeFinanceRead
	if r.Write {
		scope = middleware.ScopeFinanceWrite
	}
	chain := []any{
		middleware.RequireUserScope(scope),
		middleware.ResolveSpace(resolver),
		middleware.RequireVerb(r.Verb),
	}
	if r.Write {
		chain = append(chain, idem)
	}
	return chain
}

func registerFinance(v1 fiber.Router, d Deps, auth fiber.Handler, clock func() time.Time) {
	if d.Spaces == nil {
		return
	}
	h := &financeHandlers{bills: d.FinanceBills, imports: d.FinanceImports, jobs: d.FinanceJobs, recs: d.Recurrences, cards: d.Cards, ledger: d.Ledger, clock: clock, spaces: d.SpaceLister}
	idem := middleware.SpaceIdempotency(d.Idempotency, clock)
	fin := v1.Group("/console/finance", auth)
	mountSpaces(fin, h)
	mountFinance(fin, d.Spaces, idem, func(r financeRoute) fiber.Handler { return h.ensuringSpace(r.Handler(h)) })
}

// ensuringSpace creates the space (its settings row and system accounts) and
// seeds its default categories before the first request that reaches this
// process, a read included, so a space created before the defaults existed
// gets them the first time it is opened. EnsureReady reads before it writes, so
// a space that exists costs one read per process (and per restart), never a
// write transaction; ensureOnce keeps parallel first requests from racing. A role that may not write (a viewer) skips them and reads what exists.
func (h *financeHandlers) ensuringSpace(next fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		sp := middleware.GetSpace(c)
		err := h.ensured.do(sp.PK(), func() error {
			return h.ledger.EnsureReady(c.Context(), sp, h.now())
		})
		if err != nil && !errors.Is(err, space.ErrDenied) {
			return fail(c, err)
		}
		return next(c)
	}
}

// mountFinance registers every route of the table with its chain. The tests call
// it with probe handlers, so what they prove is this very registration — the
// order of the chain included — and not a copy of it.
func mountFinance(router fiber.Router, resolver *space.Resolver, idem fiber.Handler, handler func(financeRoute) fiber.Handler) {
	for _, r := range financeRoutes() {
		chain := append(financeChain(r, resolver, idem), handler(r))
		// Fiber runs the first argument first: the chain (scope, space, verb,
		// idempotency) must precede the handler, which is last.
		router.Add([]string{r.Method}, r.Path, chain[0], chain[1:]...)
	}
}

// decodeStrict reads the JSON body and refuses unknown fields: a typo in a
// finance request is a bug in the client that should fail loudly, not be
// silently dropped (a misspelt "category_id" must not become "no category").
func decodeStrict(c fiber.Ctx, dst any) *problem.Problem {
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return problem.BadRequest("invalid request body: " + err.Error()).WithCode("invalid_body")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return problem.BadRequest("invalid request body: content after the JSON object").WithCode("invalid_body")
	}
	return nil
}

func fieldErr(field, code, msg string, params ...any) problem.FieldError {
	return problem.Field(field, code, msg, params...)
}

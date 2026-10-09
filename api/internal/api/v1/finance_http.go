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
	bills  *services.FinanceBills
	jobs   *services.FinanceJobs
	recs   *repositories.RecurrenceRepository
	ledger *repositories.LedgerRepository
	clock  func() time.Time
	spaces spaceLister
	// ensured holds the space keys this process has already created.
	ensured sync.Map
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
		{"GET", "/reports/dre", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.dre }},
		{"GET", "/reports/cash-flow", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.cashFlow }},
		{"GET", "/settings", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.getSettings }},
		{"PUT", "/settings/default-receiving-account", space.Configure, true, func(h *financeHandlers) fiber.Handler { return h.setDefaultReceivingAccount }},
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
	h := &financeHandlers{bills: d.FinanceBills, jobs: d.FinanceJobs, recs: d.Recurrences, ledger: d.Ledger, clock: clock, spaces: d.SpaceLister}
	idem := middleware.SpaceIdempotency(d.Idempotency, clock)
	fin := v1.Group("/console/finance", auth)
	mountSpaces(fin, h)
	mountFinance(fin, d.Spaces, idem, func(r financeRoute) fiber.Handler { return h.ensuringSpace(r.Handler(h)) })
}

// ensuringSpace creates the space (its settings row and system accounts) and
// seeds its default categories before the first request that reaches this
// process, a read included, so a space created before the defaults existed
// gets them the first time it is opened. Both steps are idempotent; the
// in-memory set only saves them on every later request, and a restart costs one
// more. A role that may not write (a viewer) skips them and reads what exists.
func (h *financeHandlers) ensuringSpace(next fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		sp := middleware.GetSpace(c)
		key := sp.PK()
		if _, done := h.ensured.Load(key); !done {
			err := h.ledger.EnsureSpace(c.Context(), sp, h.now())
			if err == nil {
				err = h.ledger.SeedCategories(c.Context(), sp, h.now())
			}
			switch {
			case err == nil:
				h.ensured.Store(key, struct{}{})
			case !errors.Is(err, space.ErrDenied):
				return fail(c, err)
			}
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
		return problem.BadRequest("corpo da requisição inválido: " + err.Error())
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return problem.BadRequest("corpo da requisição inválido: conteúdo após o objeto JSON")
	}
	return nil
}

func fieldErr(field, msg, tag string) problem.FieldError {
	return problem.FieldError{Field: field, Message: msg, Tag: tag}
}

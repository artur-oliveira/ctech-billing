package v1

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/patch"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

type accountDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Class    string `json:"class"`
	DREGroup string `json:"dre_group,omitempty"`
	System   bool   `json:"system"`
	// SystemKey is the stable key of a default account; clients translate it.
	SystemKey string        `json:"system_key,omitempty"`
	Archived  bool          `json:"archived"`
	Balance   billing.Cents `json:"balance"`
}

func (h *financeHandlers) listAccounts(c fiber.Ctx) error {
	rows, err := h.ledger.ListAccounts(c.Context(), middleware.GetSpace(c))
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[accountDTO]{Data: make([]accountDTO, 0, len(rows))}
	for _, r := range rows {
		out.Data = append(out.Data, accountDTO{
			ID: r.ID, Name: r.Name, Class: string(r.Class), DREGroup: string(r.Group),
			System: r.System, SystemKey: r.SystemKey, Archived: r.Archived, Balance: r.Balance,
		})
	}
	return c.JSON(out)
}

type createAccountRequest struct {
	Name     string `json:"name"`
	Class    string `json:"class"`
	DREGroup string `json:"dre_group"`
}

// createAccount adds an account or category. The id is the server's: a client
// that chose ids could collide with the system accounts or smuggle a separator
// into a key.
func (h *financeHandlers) createAccount(c fiber.Ctx) error {
	var req createAccountRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.text("name", req.Name, true, limits.AccountName)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	a := finance.LedgerAccount{
		ID: id.New(), Name: strings.TrimSpace(req.Name), Class: finance.AccountClass(req.Class), Group: finance.DREGroup(req.DREGroup),
	}
	if err := h.ledger.CreateAccount(c.Context(), middleware.GetSpace(c), a, h.now()); err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(accountDTO{ID: a.ID, Name: a.Name, Class: string(a.Class), DREGroup: string(a.Group)})
}

func (h *financeHandlers) archiveAccount(c fiber.Ctx) error {
	if err := h.ledger.ArchiveAccount(c.Context(), middleware.GetSpace(c), c.Params("id"), h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type settingsDTO struct {
	DefaultReceivingAccountID string `json:"default_receiving_account_id,omitempty"`
}

// financeSettingsResponse is the space's settings as GET /settings answers them.
// post_ctech_invoices is always present: absent on the row means on.
type financeSettingsResponse struct {
	DefaultReceivingAccountID string `json:"default_receiving_account_id,omitempty"`
	PostCTechInvoices         bool   `json:"post_ctech_invoices"`
}

func (h *financeHandlers) getSettings(c fiber.Ctx) error {
	s, err := h.ledger.GetSettings(c.Context(), middleware.GetSpace(c))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(financeSettingsResponse{DefaultReceivingAccountID: s.DefaultReceivingAccountID, PostCTechInvoices: s.PostCTechInvoices})
}

type postCTechInvoicesRequest struct {
	PostCTechInvoices *bool `json:"post_ctech_invoices"`
}

// setPostCTechInvoices turns "Lançar minhas faturas da CTech automaticamente
// neste espaço" on or off (spec § 3.8). finance.configure, audited.
func (h *financeHandlers) setPostCTechInvoices(c fiber.Ctx) error {
	var req postCTechInvoicesRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if req.PostCTechInvoices == nil {
		ch := &checks{}
		ch.fail("post_ctech_invoices", "required", "required")
		return problem.Validation(ch.errs).Send(c)
	}
	if err := h.ledger.SetPostCTechInvoices(c.Context(), middleware.GetSpace(c), *req.PostCTechInvoices,
		actorOfUser(c), middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.JSON(map[string]bool{"post_ctech_invoices": *req.PostCTechInvoices})
}

// defaultReceivingRequest sets the default receiving account, or clears it with
// an explicit null (the PATCH rule, package patch): with none, a space's only
// active bank or cash account receives (6.7). Absent is a 422: a PUT that
// names nothing is a client bug, not "keep".
type defaultReceivingRequest struct {
	DefaultReceivingAccountID patch.Optional[string] `json:"default_receiving_account_id"`
}

func (r defaultReceivingRequest) edit() (id string, clear bool, errs []problem.FieldError) {
	ch := &checks{}
	if r.DefaultReceivingAccountID.IsNull() {
		return "", true, nil
	}
	v, _ := r.DefaultReceivingAccountID.Get()
	ch.id("default_receiving_account_id", v, true)
	return v, false, ch.errs
}

func (h *financeHandlers) setDefaultReceivingAccount(c fiber.Ctx) error {
	var req defaultReceivingRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	accountID, clear, errs := req.edit()
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	sp := middleware.GetSpace(c)
	var err error
	if clear {
		err = h.ledger.ClearDefaultReceivingAccount(c.Context(), sp, h.now())
	} else {
		err = h.ledger.SetDefaultReceivingAccount(c.Context(), sp, accountID, h.now())
	}
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(settingsDTO{DefaultReceivingAccountID: accountID})
}

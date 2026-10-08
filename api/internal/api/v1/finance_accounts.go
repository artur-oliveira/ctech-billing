package v1

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

type accountDTO struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Class    string        `json:"class"`
	DREGroup string        `json:"dre_group,omitempty"`
	System   bool          `json:"system"`
	Archived bool          `json:"archived"`
	Balance  billing.Cents `json:"balance"`
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
			System: r.System, Archived: r.Archived, Balance: r.Balance,
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
	var errs []problem.FieldError
	if strings.TrimSpace(req.Name) == "" {
		errs = append(errs, fieldErr("name", "obrigatório", "required"))
	}
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	a := finance.LedgerAccount{
		ID: id.New(), Name: req.Name, Class: finance.AccountClass(req.Class), Group: finance.DREGroup(req.DREGroup),
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

func (h *financeHandlers) getSettings(c fiber.Ctx) error {
	s, err := h.ledger.GetSettings(c.Context(), middleware.GetSpace(c))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(settingsDTO{DefaultReceivingAccountID: s.DefaultReceivingAccountID})
}

func (h *financeHandlers) setDefaultReceivingAccount(c fiber.Ctx) error {
	var req settingsDTO
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if req.DefaultReceivingAccountID == "" {
		return problem.Validation([]problem.FieldError{fieldErr("default_receiving_account_id", "obrigatório", "required")}).Send(c)
	}
	if err := h.ledger.SetDefaultReceivingAccount(c.Context(), middleware.GetSpace(c), req.DefaultReceivingAccountID, h.now()); err != nil {
		return fail(c, err)
	}
	return c.JSON(settingsDTO{DefaultReceivingAccountID: req.DefaultReceivingAccountID})
}

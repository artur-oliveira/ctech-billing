package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// A synced bank account's lines still on "A classificar" (BYOK spec § 8.3).
type unclassifiedDTO struct {
	TxID      string        `json:"tx_id"`
	Date      brcal.Date    `json:"date"`
	Amount    billing.Cents `json:"amount"`
	Direction string        `json:"direction"` // credit | debit
	Memo      string        `json:"memo,omitempty"`
}

func (h *financeHandlers) listUnclassified(c fiber.Ctx) error {
	lines, err := h.ledger.Unclassified(c.Context(), middleware.GetSpace(c), c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[unclassifiedDTO]{Data: make([]unclassifiedDTO, 0, len(lines))}
	for _, l := range lines {
		dir := "debit"
		if l.Credit {
			dir = "credit"
		}
		out.Data = append(out.Data, unclassifiedDTO{TxID: l.TxID, Date: l.Date, Amount: l.Amount, Direction: dir, Memo: l.Memo})
	}
	return c.JSON(out)
}

type classifyRequest struct {
	CategoryID        string `json:"category_id"`
	BillID            string `json:"bill_id"`
	TransferAccountID string `json:"transfer_account_id"`
}

// classify replaces an unclassified bank line: a category, an open bill or a
// transfer, exactly one. The bank account's balance never moves.
func (h *financeHandlers) classify(c fiber.Ctx) error {
	var req classifyRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	set := 0
	for _, v := range []string{req.CategoryID, req.BillID, req.TransferAccountID} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return problem.Validation([]problem.FieldError{fieldErr("category_id", "exactly_one",
			"choose exactly one of category_id, bill_id and transfer_account_id")}).Send(c)
	}
	txID, err := h.ledger.Classify(c.Context(), h.billsRepo, middleware.GetSpace(c), c.Params("tx_id"),
		repositories.Classification{CategoryID: req.CategoryID, BillID: req.BillID, TransferAccountID: req.TransferAccountID},
		h.postMeta(c, ""), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"tx_id": txID})
}

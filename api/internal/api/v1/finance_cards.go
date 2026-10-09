package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type cardDTO struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	ClosingDay      int           `json:"closing_day"`
	DueDay          int           `json:"due_day"`
	PayingAccountID string        `json:"paying_account_id"`
	OpenMonth       string        `json:"open_month"`
	Balance         billing.Cents `json:"balance"` // the card account's: negative is owed
	Archived        bool          `json:"archived"`
}

func newCardDTO(c repositories.CardRow) cardDTO {
	return cardDTO{ID: c.ID, Name: c.Name, ClosingDay: c.ClosingDay, DueDay: c.DueDay, PayingAccountID: c.PayingAccountID,
		OpenMonth: c.OpenMonth.String(), Balance: billing.Cents(c.Balance), Archived: c.Archived}
}

type statementItemDTO struct {
	PurchaseID  string        `json:"purchase_id"`
	Description string        `json:"description"`
	CategoryID  string        `json:"category_id,omitempty"`
	Date        string        `json:"date"`
	Number      int           `json:"number,omitempty"`
	Of          int           `json:"of,omitempty"`
	Kind        string        `json:"kind"`
	Amount      billing.Cents `json:"amount"`
}

type cardStatementDTO struct {
	CardID      string             `json:"card_id"`
	Month       string             `json:"month"`
	Status      string             `json:"status"` // open | future | closed | paid
	ClosingDate string             `json:"closing_date"`
	DueDate     string             `json:"due_date"`
	Total       billing.Cents      `json:"total"`
	BillID      string             `json:"bill_id,omitempty"`
	Items       []statementItemDTO `json:"items"`
}

type installmentDTO struct {
	Number int           `json:"number"`
	Amount billing.Cents `json:"amount"`
	Month  string        `json:"month"`
}

type purchaseDTO struct {
	ID           string           `json:"id"`
	Description  string           `json:"description"`
	CategoryID   string           `json:"category_id"`
	Date         string           `json:"date"`
	Total        billing.Cents    `json:"total"`
	Installments []installmentDTO `json:"installments"`
	Refunded     bool             `json:"refunded"`
}

func newPurchaseDTO(p repositories.Purchase) purchaseDTO {
	out := purchaseDTO{ID: p.ID, Description: p.Description, CategoryID: p.CategoryID, Date: p.Date.String(),
		Total: p.Total, Refunded: p.Refunded, Installments: make([]installmentDTO, len(p.Installments))}
	for i, inst := range p.Installments {
		out.Installments[i] = installmentDTO{Number: inst.Number, Amount: inst.Amount, Month: inst.Statement.String()}
	}
	return out
}

// statementDTO reports "paid" for a closed statement whose bill was paid: the
// statement row stays closed (it is never changed); the payment is the bill's.
func (h *financeHandlers) statementDTO(c fiber.Ctx, s repositories.Statement) (cardStatementDTO, error) {
	out := cardStatementDTO{CardID: s.CardID, Month: s.Month.String(), Status: s.Status, ClosingDate: s.ClosingDate.String(),
		DueDate: s.DueDate.String(), Total: s.Total, BillID: s.BillID, Items: make([]statementItemDTO, len(s.Items))}
	for i, it := range s.Items {
		out.Items[i] = statementItemDTO{PurchaseID: it.PurchaseID, Description: it.Description, CategoryID: it.CategoryID,
			Date: it.Date.String(), Number: it.Number, Of: it.Of, Kind: it.Kind, Amount: it.Amount}
	}
	if s.BillID != "" {
		b, err := h.bills.Get(c.Context(), middleware.GetSpace(c), s.BillID)
		if err != nil {
			return out, err
		}
		if b.Status == finance.BillPaid {
			out.Status = "paid"
		}
	}
	return out, nil
}

type cardRequest struct {
	Name            string `json:"name"`
	ClosingDay      int    `json:"closing_day"`
	DueDay          int    `json:"due_day"`
	PayingAccountID string `json:"paying_account_id"`
}

func (r cardRequest) validate() []problem.FieldError {
	c := &checks{}
	c.text("name", r.Name, true, limits.AccountName)
	if r.ClosingDay < 1 || r.ClosingDay > 31 {
		c.fail("closing_day", "um dia entre 1 e 31", "range")
	}
	if r.DueDay < 1 || r.DueDay > 31 {
		c.fail("due_day", "um dia entre 1 e 31", "range")
	}
	c.id("paying_account_id", r.PayingAccountID, true)
	return c.errs
}

func (h *financeHandlers) listCards(c fiber.Ctx) error {
	rows, err := h.cards.ListCards(c.Context(), middleware.GetSpace(c))
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[cardDTO]{Data: make([]cardDTO, 0, len(rows))}
	for _, r := range rows {
		out.Data = append(out.Data, newCardDTO(r))
	}
	return c.JSON(out)
}

func (h *financeHandlers) createCard(c fiber.Ctx) error {
	var req cardRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if errs := req.validate(); len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	row, err := h.cards.CreateCard(c.Context(), middleware.GetSpace(c), req.Name,
		finance.Card{ClosingDay: req.ClosingDay, DueDay: req.DueDay, PayingAccountID: req.PayingAccountID}, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newCardDTO(row))
}

type cardPatchRequest struct {
	ClosingDay      *int    `json:"closing_day"`
	DueDay          *int    `json:"due_day"`
	PayingAccountID *string `json:"paying_account_id"`
}

func (h *financeHandlers) patchCard(c fiber.Ctx) error {
	var req cardPatchRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	if req.ClosingDay != nil && (*req.ClosingDay < 1 || *req.ClosingDay > 31) {
		ch.fail("closing_day", "um dia entre 1 e 31", "range")
	}
	if req.DueDay != nil && (*req.DueDay < 1 || *req.DueDay > 31) {
		ch.fail("due_day", "um dia entre 1 e 31", "range")
	}
	if req.PayingAccountID != nil {
		ch.id("paying_account_id", *req.PayingAccountID, true)
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	row, err := h.cards.UpdateCard(c.Context(), middleware.GetSpace(c), c.Params("id"),
		repositories.CardPatch{ClosingDay: req.ClosingDay, DueDay: req.DueDay, PayingAccountID: req.PayingAccountID}, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newCardDTO(row))
}

func (h *financeHandlers) cardStatement(c fiber.Ctx) error {
	m, err := finance.ParseMonth(c.Params("month"))
	if err != nil {
		return problem.Validation([]problem.FieldError{fieldErr("month", "use YYYY-MM", "format")}).Send(c)
	}
	s, err := h.cards.GetStatement(c.Context(), middleware.GetSpace(c), c.Params("id"), m)
	if err != nil {
		return fail(c, err)
	}
	out, err := h.statementDTO(c, s)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(out)
}

func (h *financeHandlers) listPurchases(c fiber.Ctx) error {
	ps, err := h.cards.ListPurchases(c.Context(), middleware.GetSpace(c), c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[purchaseDTO]{Data: make([]purchaseDTO, 0, len(ps))}
	for _, p := range ps {
		out.Data = append(out.Data, newPurchaseDTO(p))
	}
	return c.JSON(out)
}

type purchaseRequest struct {
	Date         string        `json:"date"`
	Description  string        `json:"description"`
	CategoryID   string        `json:"category_id"`
	Total        billing.Cents `json:"total"`
	Installments int           `json:"installments"`
}

func (r purchaseRequest) validate(today brcal.Date) []problem.FieldError {
	c := &checks{}
	if d, e := parseDay("date", r.Date); e != nil {
		c.errs = append(c.errs, *e)
	} else {
		// A purchase already happened: never dated in the future.
		c.date("date", d, limits.MinDate, today)
	}
	c.text("description", r.Description, true, limits.Description)
	c.id("category_id", r.CategoryID, true)
	c.amount("total", r.Total)
	switch {
	case r.Installments < 1 || r.Installments > limits.MaxInstallments:
		c.fail("installments", "entre 1 e 48 parcelas", "range")
	case r.Total > 0 && r.Total < billing.Cents(r.Installments):
		c.fail("installments", "cada parcela precisa de pelo menos R$ 0,01", "range")
	}
	return c.errs
}

func (h *financeHandlers) createPurchase(c fiber.Ctx) error {
	var req purchaseRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if errs := req.validate(h.today()); len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	date, _ := brcal.Parse(req.Date)
	p, err := h.cards.AddPurchase(c.Context(), middleware.GetSpace(c), c.Params("id"), repositories.Purchase{
		Date: date, Description: req.Description, CategoryID: req.CategoryID, Total: req.Total,
		Installments: make([]finance.Installment, req.Installments),
	}, h.postMeta(c, ""), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newPurchaseDTO(p))
}

func (h *financeHandlers) refundPurchase(c fiber.Ctx) error {
	p, err := h.cards.Refund(c.Context(), middleware.GetSpace(c), c.Params("id"), c.Params("pid"), h.postMeta(c, ""), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newPurchaseDTO(p))
}

func (h *financeHandlers) advancePurchase(c fiber.Ctx) error {
	p, err := h.cards.Advance(c.Context(), middleware.GetSpace(c), c.Params("id"), c.Params("pid"), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newPurchaseDTO(p))
}

type closeRequest struct {
	Month string `json:"month"`
}

func (h *financeHandlers) closeStatement(c fiber.Ctx) error {
	var req closeRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	m, err := finance.ParseMonth(req.Month)
	if err != nil {
		return problem.Validation([]problem.FieldError{fieldErr("month", "use YYYY-MM", "format")}).Send(c)
	}
	s, err := h.cards.CloseNow(c.Context(), middleware.GetSpace(c), c.Params("id"), m, h.now())
	if err != nil {
		return fail(c, err)
	}
	out, err := h.statementDTO(c, s)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(out)
}

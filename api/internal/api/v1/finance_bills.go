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

type billDTO struct {
	ID             string        `json:"id"`
	Direction      string        `json:"direction"`
	Amount         billing.Cents `json:"amount"`
	AccountID      string        `json:"account_id"`
	CategoryID     string        `json:"category_id"`
	Description    string        `json:"description,omitempty"`
	CompetenceDate brcal.Date    `json:"competence_date"`
	DueDate        brcal.Date    `json:"due_date"`
	PaidDate       *brcal.Date   `json:"paid_date,omitempty"`
	Status         string        `json:"status"`
	Origin         string        `json:"origin"`
	OriginRef      string        `json:"origin_ref,omitempty"`
	AutoSettle     bool          `json:"auto_settle"`
	// Bucket places an open bill relative to today: overdue, today or upcoming.
	Bucket string `json:"bucket,omitempty"`
}

func newBillDTO(b finance.Bill, today brcal.Date) billDTO {
	d := billDTO{
		ID: b.ID, Direction: string(b.Direction), Amount: b.Amount, AccountID: b.AccountID, CategoryID: b.CategoryID,
		Description: b.Description, CompetenceDate: b.Competence, DueDate: b.Due, Status: string(b.Status),
		Origin: string(b.Origin), OriginRef: b.OriginRef, AutoSettle: b.AutoSettle,
	}
	if !b.PaidDate.IsZero() {
		paid := b.PaidDate
		d.PaidDate = &paid
	}
	if b.Status == finance.BillForecast {
		d.Bucket = string(b.BucketOn(today))
	}
	return d
}

type createBillRequest struct {
	Direction      string        `json:"direction"`
	Amount         billing.Cents `json:"amount"`
	AccountID      string        `json:"account_id"`
	CategoryID     string        `json:"category_id"`
	Description    string        `json:"description"`
	CompetenceDate *brcal.Date   `json:"competence_date"`
	DueDate        brcal.Date    `json:"due_date"`
	AutoSettle     bool          `json:"auto_settle"`
}

func (r createBillRequest) validate(today brcal.Date) []problem.FieldError {
	c := &checks{}
	if r.Direction != string(finance.Payable) && r.Direction != string(finance.Receivable) {
		c.fail("direction", "unsupported_value", "use payable or receivable", "allowed", []string{"payable", "receivable"})
	}
	c.amount("amount", r.Amount)
	c.id("account_id", r.AccountID, true)
	c.id("category_id", r.CategoryID, true)
	c.text("description", r.Description, false, limits.Description)
	c.date("due_date", r.DueDate, limits.MinDate, limits.MaxDate(today))
	if r.CompetenceDate != nil && !r.CompetenceDate.IsZero() {
		c.date("competence_date", *r.CompetenceDate, limits.MinDate, limits.MaxDate(today))
	}
	return c.errs
}

func (h *financeHandlers) createBill(c fiber.Ctx) error {
	var req createBillRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if errs := req.validate(h.today()); len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	competence := req.DueDate
	if req.CompetenceDate != nil && !req.CompetenceDate.IsZero() {
		competence = *req.CompetenceDate
	}
	b, err := h.bills.Create(c.Context(), middleware.GetSpace(c), finance.Bill{
		Direction: finance.Direction(req.Direction), Amount: req.Amount, AccountID: req.AccountID,
		CategoryID: req.CategoryID, Description: req.Description, Competence: competence, Due: req.DueDate,
		Origin: finance.OriginManual, AutoSettle: req.AutoSettle,
	}, actorOfUser(c), middleware.GetRequestID(c), c.Get(middleware.IdempotencyHeader), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newBillDTO(b, h.today()))
}

func (h *financeHandlers) getBill(c fiber.Ctx) error {
	b, err := h.bills.Get(c.Context(), middleware.GetSpace(c), c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newBillDTO(*b, h.today()))
}

func (h *financeHandlers) listBills(c fiber.Ctx) error {
	dir := finance.Direction(c.Query("direction"))
	if dir != finance.Payable && dir != finance.Receivable {
		return problem.Validation([]problem.FieldError{fieldErr("direction", "unsupported_value", "use payable or receivable", "allowed", []string{"payable", "receivable"})}).Send(c)
	}
	limit := fiber.Query(c, "limit", 50)
	if limit < 1 || limit > pageLimit {
		return problem.Validation([]problem.FieldError{fieldErr("limit", "out_of_range", "between 1 and 100", "min", 1, "max", 100)}).Send(c)
	}
	start, err := repositories.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return problem.BadRequest("invalid cursor").WithCode("invalid_cursor").Send(c)
	}
	page, err := h.bills.ListOpen(c.Context(), middleware.GetSpace(c), dir, limit, start)
	if err != nil {
		return fail(c, err)
	}
	today := h.today()
	out := listResponse[billDTO]{Data: make([]billDTO, 0, len(page.Items))}
	for _, b := range page.Items {
		out.Data = append(out.Data, newBillDTO(b, today))
	}
	if len(page.LastEvaluatedKey) > 0 {
		out.HasMore, out.Cursor = true, repositories.EncodeCursor(page.LastEvaluatedKey)
	}
	return c.JSON(out)
}

type patchBillRequest struct {
	Amount      *billing.Cents `json:"amount"`
	CategoryID  *string        `json:"category_id"`
	AccountID   *string        `json:"account_id"`
	Description *string        `json:"description"`
	DueDate     *brcal.Date    `json:"due_date"`
	AutoSettle  *bool          `json:"auto_settle"`
}

func (h *financeHandlers) patchBill(c fiber.Ctx) error {
	var req patchBillRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if errs := req.validate(h.today()); len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	b, err := h.bills.Edit(c.Context(), middleware.GetSpace(c), c.Params("id"), repositories.BillEdit{
		Amount: req.Amount, CategoryID: req.CategoryID, AccountID: req.AccountID,
		Description: req.Description, Due: req.DueDate, AutoSettle: req.AutoSettle,
	}, actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newBillDTO(b, h.today()))
}

func (r patchBillRequest) validate(today brcal.Date) []problem.FieldError {
	c := &checks{}
	if r.Amount != nil {
		c.amount("amount", *r.Amount)
	}
	if r.CategoryID != nil {
		c.id("category_id", *r.CategoryID, true)
	}
	if r.AccountID != nil {
		c.id("account_id", *r.AccountID, true)
	}
	if r.Description != nil {
		c.text("description", *r.Description, false, limits.Description)
	}
	if r.DueDate != nil {
		c.date("due_date", *r.DueDate, limits.MinDate, limits.MaxDate(today))
	}
	return c.errs
}

type settleBillRequest struct {
	PaidAmount           *billing.Cents `json:"paid_amount"`
	PaidDate             *brcal.Date    `json:"paid_date"`
	DifferenceCategoryID string         `json:"difference_category_id"`
}

func (h *financeHandlers) settleBill(c fiber.Ctx) error {
	var req settleBillRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	sp := middleware.GetSpace(c)
	ch := &checks{}
	if req.PaidAmount != nil {
		ch.amount("paid_amount", *req.PaidAmount)
	}
	if req.PaidDate != nil {
		// Paying is cash that already moved: never in the future.
		ch.date("paid_date", *req.PaidDate, limits.MinDate, h.today())
	}
	ch.id("difference_category_id", req.DifferenceCategoryID, false)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	var paid billing.Cents
	if req.PaidAmount != nil {
		paid = *req.PaidAmount
		if req.DifferenceCategoryID == "" {
			// A different amount needs a category for the gap (interest, discount).
			bill, err := h.bills.Get(c.Context(), sp, c.Params("id"))
			if err != nil {
				return fail(c, err)
			}
			if e := settleGapError(paid, bill.Amount, req.DifferenceCategoryID); e != nil {
				return problem.Validation([]problem.FieldError{*e}).Send(c)
			}
		}
	}
	var date brcal.Date
	if req.PaidDate != nil {
		date = *req.PaidDate
	}
	b, err := h.bills.Settle(c.Context(), sp, c.Params("id"), paid, req.DifferenceCategoryID, date,
		actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newBillDTO(b, h.today()))
}

func (h *financeHandlers) cancelBill(c fiber.Ctx) error {
	b, err := h.bills.Cancel(c.Context(), middleware.GetSpace(c), c.Params("id"), actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newBillDTO(b, h.today()))
}

// settleGapError is the rule "a paid amount different from the bill's needs a
// category for the difference", apart from the handler so it is testable without
// a bill to read.
func settleGapError(paid, billAmount billing.Cents, categoryID string) *problem.FieldError {
	if paid == billAmount || categoryID != "" {
		return nil
	}
	e := fieldErr("difference_category_id", "required_with", "required when the amount paid differs from the bill amount", "other", "amount")
	return &e
}

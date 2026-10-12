package v1

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/patch"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type recurrenceDTO struct {
	ID          string             `json:"id"`
	Direction   string             `json:"direction"`
	Amount      billing.Cents      `json:"amount"`
	AmountMode  finance.AmountMode `json:"amount_mode"`
	CategoryID  string             `json:"category_id"`
	AccountID   string             `json:"account_id"`
	Description string             `json:"description,omitempty"`
	Expression  json.RawMessage    `json:"expression"`
	Start       brcal.Date         `json:"start"`
	End         *brcal.Date        `json:"end,omitempty"`
	Adjust      string             `json:"business_day_adjust"`
	AutoSettle  bool               `json:"auto_settle"`
	Archived    bool               `json:"archived"`
	// CardID is set instead of AccountID on a recurrence paid by card.
	CardID         string `json:"card_id,omitempty"`
	Installments   int    `json:"installments,omitempty"`
	NeedsAttention string `json:"needs_attention,omitempty"`
}

func newRecurrenceDTO(r finance.Recurrence) (recurrenceDTO, error) {
	expr, err := finance.MarshalExpression(r.Schedule.Expression)
	if err != nil {
		return recurrenceDTO{}, err
	}
	d := recurrenceDTO{
		ID: r.ID, Direction: string(r.Direction), Amount: r.Amount, AmountMode: r.AmountMode, CategoryID: r.CategoryID, AccountID: r.AccountID,
		Description: r.Description, Expression: expr, Start: r.Schedule.Start, Adjust: string(r.Schedule.Adjust),
		AutoSettle: r.AutoSettle, Archived: r.Archived,
		CardID: r.CardID, Installments: r.Installments, NeedsAttention: r.NeedsAttention,
	}
	if !r.Schedule.End.IsZero() {
		end := r.Schedule.End
		d.End = &end
	}
	return d, nil
}

// scheduleRequest is the part of a request that describes when. The expression
// is the 6.1 tagged JSON, parsed through the same validator as stored ones.
type scheduleRequest struct {
	Expression json.RawMessage `json:"expression"`
	Start      brcal.Date      `json:"start"`
	End        *brcal.Date     `json:"end"`
	Adjust     string          `json:"business_day_adjust"`
}

// schedule parses and bounds a rule: it starts between limits.MinDate and ten
// years from today, and ends (when it ends) after it starts and within fifty
// years of it — enough for a mortgage, short of a typo in the year.
func (s scheduleRequest) schedule(today brcal.Date) (finance.Schedule, []problem.FieldError) {
	var errs []problem.FieldError
	c := &checks{}
	c.date("start", s.Start, limits.MinDate, limits.MaxDate(today))
	if s.End != nil && !s.End.IsZero() && !s.Start.IsZero() {
		c.date("end", *s.End, s.Start, s.Start.AddYears(limits.MaxRecurrenceYears))
	}
	errs = append(errs, c.errs...)
	if len(s.Expression) == 0 {
		return finance.Schedule{}, []problem.FieldError{fieldErr("expression", "required", "required")}
	}
	expr, err := finance.ParseSchedule(s.Expression)
	if err != nil {
		errs = append(errs, fieldErr("expression", "invalid_expression", err.Error()))
	}
	adjust := finance.BusinessDayAdjust(s.Adjust)
	if adjust == "" {
		adjust = finance.AdjustNone
	}
	sched := finance.Schedule{Expression: expr, Start: s.Start, Adjust: adjust}
	if s.End != nil {
		sched.End = *s.End
	}
	return sched, errs
}

type createRecurrenceRequest struct {
	scheduleRequest
	Direction   string             `json:"direction"`
	Amount      billing.Cents      `json:"amount"`
	AmountMode  finance.AmountMode `json:"amount_mode"`
	CategoryID  string             `json:"category_id"`
	AccountID   string             `json:"account_id"`
	Description string             `json:"description"`
	AutoSettle  bool               `json:"auto_settle"`
	// CardID pays the recurrence by card instead of AccountID (exactly one);
	// Installments splits each occurrence (card only, default 1).
	CardID       string `json:"card_id"`
	Installments int    `json:"installments"`
}

// installmentChecks is the installments rule of a purchase and of a card
// recurrence: 1..48, each at least 0,01.
func installmentChecks(c *checks, total billing.Cents, n int) {
	switch {
	case n < 1 || n > limits.MaxInstallments:
		c.fail("installments", "out_of_range", "between 1 and 48 installments", "min", 1, "max", limits.MaxInstallments)
	case total > 0 && total < billing.Cents(n):
		c.fail("installments", "installment_too_small", "each installment needs at least 0.01")
	}
}

func (h *financeHandlers) createRecurrence(c fiber.Ctx) error {
	var req createRecurrenceRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	sched, errs := req.scheduleRequest.schedule(h.today())
	if req.Direction != string(finance.Payable) && req.Direction != string(finance.Receivable) {
		errs = append(errs, fieldErr("direction", "unsupported_value", "use payable or receivable", "allowed", []string{"payable", "receivable"}))
	}
	if req.AmountMode == "" {
		req.AmountMode = finance.AmountFixed
	}
	if req.AmountMode != finance.AmountFixed && req.AmountMode != finance.AmountExpected {
		errs = append(errs, fieldErr("amount_mode", "unsupported_value", "use fixed or expected", "allowed", []string{string(finance.AmountFixed), string(finance.AmountExpected)}))
	}
	ch := &checks{}
	ch.amount("amount", req.Amount)
	ch.id("category_id", req.CategoryID, true)
	switch {
	case req.AccountID != "" && req.CardID != "":
		ch.fail("card_id", "conflict", "pay with an account or a card, not both")
	case req.AccountID == "" && req.CardID == "":
		ch.fail("account_id", "required", "an account or a card is required")
	case req.CardID != "":
		ch.id("card_id", req.CardID, true)
		if req.Direction == string(finance.Receivable) {
			ch.fail("card_id", "payable_only", "a card pays expenses only")
		}
		if req.AutoSettle {
			ch.fail("auto_settle", "not_with_card", "the card's statement is what gets paid")
		}
		if req.Installments == 0 {
			req.Installments = 1
		}
		installmentChecks(ch, req.Amount, req.Installments)
	default:
		ch.id("account_id", req.AccountID, true)
		if req.Installments != 0 {
			ch.fail("installments", "card_only", "installments are for a card")
		}
	}
	ch.text("description", req.Description, false, limits.Description)
	errs = append(errs, ch.errs...)
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	rec, err := h.recs.Create(c.Context(), middleware.GetSpace(c), finance.Recurrence{
		Direction: finance.Direction(req.Direction), Amount: req.Amount, AmountMode: req.AmountMode, CategoryID: req.CategoryID,
		AccountID: req.AccountID, Description: req.Description, Schedule: sched, AutoSettle: req.AutoSettle,
		CardID: req.CardID, Installments: req.Installments,
	}, repositories.PostMeta{Actor: actorOfUser(c), RequestID: middleware.GetRequestID(c)}, h.now())
	if err != nil {
		return fail(c, err)
	}
	dto, err := newRecurrenceDTO(rec)
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(dto)
}

func (h *financeHandlers) listRecurrences(c fiber.Ctx) error {
	recs, err := h.recs.List(c.Context(), middleware.GetSpace(c))
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[recurrenceDTO]{Data: make([]recurrenceDTO, 0, len(recs))}
	for _, r := range recs {
		dto, err := newRecurrenceDTO(r)
		if err != nil {
			return fail(c, err)
		}
		out.Data = append(out.Data, dto)
	}
	return c.JSON(out)
}

type pendingOccurrenceDTO struct {
	RecurrenceID string            `json:"recurrence_id"`
	Nominal      brcal.Date        `json:"nominal"`
	Due          brcal.Date        `json:"due"`
	Direction    finance.Direction `json:"direction"`
	Expected     billing.Cents     `json:"expected_amount"`
	AccountID    string            `json:"account_id,omitempty"`
	CardID       string            `json:"card_id,omitempty"`
	CategoryID   string            `json:"category_id"`
	Description  string            `json:"description,omitempty"`
	Installments int               `json:"installments,omitempty"`
}

func pendingDTO(p repositories.PendingOccurrence) pendingOccurrenceDTO {
	return pendingOccurrenceDTO{RecurrenceID: p.RecurrenceID, Nominal: p.Nominal, Due: p.Due, Direction: p.Direction, Expected: p.Expected, AccountID: p.AccountID, CardID: p.CardID, CategoryID: p.CategoryID, Description: p.Description, Installments: p.Installments}
}

func (h *financeHandlers) listPendingOccurrences(c fiber.Ctx) error {
	rows, err := h.recs.ListPendingOccurrences(c.Context(), middleware.GetSpace(c), 50)
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[pendingOccurrenceDTO]{Data: make([]pendingOccurrenceDTO, 0, len(rows))}
	for _, row := range rows {
		out.Data = append(out.Data, pendingDTO(row))
	}
	return c.JSON(out)
}

type confirmOccurrenceRequest struct {
	Amount billing.Cents `json:"amount"`
}

type occurrenceActionResponse struct {
	State      string        `json:"state"`
	Amount     billing.Cents `json:"amount,omitempty"`
	BillID     string        `json:"bill_id,omitempty"`
	PurchaseID string        `json:"purchase_id,omitempty"`
}

func (h *financeHandlers) confirmExpectedOccurrence(c fiber.Ctx) error {
	var req confirmOccurrenceRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	validation := &checks{}
	validation.amount("amount", req.Amount)
	if len(validation.errs) > 0 {
		return problem.Validation(validation.errs).Send(c)
	}
	sp := middleware.GetSpace(c)
	nominal, err := brcal.Parse(c.Params("nominal"))
	if err != nil {
		return problem.Validation([]problem.FieldError{fieldErr("nominal", "invalid_date", "must be a valid date")}).Send(c)
	}
	pending, err := h.recs.GetPendingOccurrence(c.Context(), sp, c.Params("id"), nominal)
	if err != nil {
		return fail(c, err)
	}
	if pending.CardID != "" {
		installmentCheck := &checks{}
		installmentChecks(installmentCheck, req.Amount, pending.Installments)
		if len(installmentCheck.errs) > 0 {
			return problem.Validation(installmentCheck.errs).Send(c)
		}
	}
	actor, requestID, key := actorOfUser(c), middleware.GetRequestID(c), c.Get(middleware.IdempotencyHeader)
	if pending.CardID != "" {
		meta := repositories.PostMeta{Origin: "recurrence_confirmation", Actor: actor, RequestID: requestID, IdempotencyKey: key}
		created, purchase, err := h.cards.ConfirmExpectedOccurrence(c.Context(), sp, pending, req.Amount, meta, h.now())
		if err != nil {
			return fail(c, err)
		}
		status := fiber.StatusCreated
		if !created {
			status = fiber.StatusOK
		}
		return c.Status(status).JSON(occurrenceActionResponse{State: "confirmed", Amount: req.Amount, PurchaseID: purchase.ID})
	}
	created, bill, err := h.bills.ConfirmExpected(c.Context(), sp, pending, req.Amount, actor, requestID, key, h.now())
	if err != nil {
		return fail(c, err)
	}
	status := fiber.StatusCreated
	if !created {
		status = fiber.StatusOK
	}
	return c.Status(status).JSON(occurrenceActionResponse{State: "confirmed", Amount: req.Amount, BillID: bill.ID})
}

func (h *financeHandlers) skipExpectedOccurrence(c fiber.Ctx) error {
	nominal, err := brcal.Parse(c.Params("nominal"))
	if err != nil {
		return problem.Validation([]problem.FieldError{fieldErr("nominal", "invalid_date", "must be a valid date")}).Send(c)
	}
	_, err = h.recs.SkipPendingOccurrence(c.Context(), middleware.GetSpace(c), c.Params("id"), nominal, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(occurrenceActionResponse{State: "skipped"})
}

type patchRecurrenceRequest struct {
	Amount      patch.Optional[billing.Cents] `json:"amount"`
	CategoryID  patch.Optional[string]        `json:"category_id"`
	AccountID   patch.Optional[string]        `json:"account_id"`
	Description patch.Optional[string]        `json:"description"`
	AutoSettle  patch.Optional[bool]          `json:"auto_settle"`
	End         patch.Optional[brcal.Date]    `json:"end"`
	// CardID switches the recurrence to a card (AccountID switches it back);
	// Installments is the card's split. Null on either is required.
	CardID       patch.Optional[string] `json:"card_id"`
	Installments patch.Optional[int]    `json:"installments"`
	// Archive confirms an end that leaves nothing to come: the edit and the
	// archive are one write. Without it such an end is 422 recurrence_would_end.
	Archive bool `json:"archive"`
	// CancelAfterEnd confirms that an end date with unpaid bills made after it
	// cancels them (UX batch 5 review, I2). Without it such an end is 422
	// end_cancels_bills and nothing is saved.
	CancelAfterEnd bool `json:"cancel_after_end"`
}

// edit validates the body under the PATCH rule (package patch): absent keeps,
// null clears the end or the description, null on anything else is required.
func (r patchRecurrenceRequest) edit(today brcal.Date) (repositories.RecurrencePatch, []problem.FieldError) {
	ch := &checks{}
	if v, ok := required(ch, "amount", r.Amount); ok {
		ch.amount("amount", v)
	}
	if v, ok := required(ch, "category_id", r.CategoryID); ok {
		ch.id("category_id", v, true)
	}
	if v, ok := required(ch, "account_id", r.AccountID); ok {
		ch.id("account_id", v, true)
	}
	if v, ok := required(ch, "card_id", r.CardID); ok {
		ch.id("card_id", v, true)
	}
	if v, ok := required(ch, "installments", r.Installments); ok && (v < 1 || v > limits.MaxInstallments) {
		ch.fail("installments", "out_of_range", "between 1 and 48 installments", "min", 1, "max", limits.MaxInstallments)
	}
	if acct, a := r.AccountID.Get(); a && acct != "" {
		if card, c := r.CardID.Get(); c && card != "" {
			ch.fail("card_id", "conflict", "pay with an account or a card, not both")
		}
	}
	required(ch, "auto_settle", r.AutoSettle)
	description := clearable(r.Description)
	if description != nil {
		ch.text("description", *description, false, limits.Description)
	}
	if v, ok := r.End.Get(); ok {
		ch.date("end", v, limits.MinDate, today.AddYears(limits.MaxRecurrenceYears))
	}
	return repositories.RecurrencePatch{
		Amount: r.Amount.Ptr(), CategoryID: r.CategoryID.Ptr(), AccountID: r.AccountID.Ptr(),
		Description: description, AutoSettle: r.AutoSettle.Ptr(), End: r.End, Archive: r.Archive,
		CardID: r.CardID.Ptr(), Installments: r.Installments.Ptr(),
	}, ch.errs
}

func (h *financeHandlers) patchRecurrence(c fiber.Ctx) error {
	var req patchRecurrenceRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	edit, errs := req.edit(h.today())
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	sp := middleware.GetSpace(c)
	end, hasEnd := req.End.Get()
	// An end date with unpaid bills made after it cancels them (UX batch 5,
	// review I2), whether or not the rule ends: asked first, before anything is
	// written. The bills are read in the resolved space only.
	if hasEnd && !req.CancelAfterEnd {
		n, err := h.bills.UnpaidAfter(c.Context(), sp, c.Params("id"), end)
		if err != nil {
			return fail(c, err)
		}
		if n > 0 {
			return fail(c, repositories.ErrEndCancelsBills)
		}
	}
	if err := h.recs.Update(c.Context(), sp, c.Params("id"), edit, h.now()); err != nil {
		return fail(c, err)
	}
	// Then the confirmed cancellations, after the end (and the archive) are
	// written. A failure half-way is 409 recurrence_end_incomplete with the end
	// saved; the same PATCH again finishes the job (FinanceBills.EndRecurrence).
	out := patchRecurrenceResponse{CanceledBillIDs: []string{}}
	if hasEnd && req.CancelAfterEnd {
		canceled, err := h.bills.EndRecurrence(c.Context(), sp, c.Params("id"), end, actorOfUser(c), middleware.GetRequestID(c), h.now())
		if err != nil {
			return fail(c, err)
		}
		out.CanceledBillIDs = canceled
	}
	rec, err := h.recs.Get(c.Context(), sp, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if out.recurrenceDTO, err = newRecurrenceDTO(*rec); err != nil {
		return fail(c, err)
	}
	return c.JSON(out)
}

// patchRecurrenceResponse is the recurrence as edited, plus the bills an end
// with archive cancelled (empty otherwise).
type patchRecurrenceResponse struct {
	recurrenceDTO
	CanceledBillIDs []string `json:"canceled_bill_ids"`
}

func (h *financeHandlers) archiveRecurrence(c fiber.Ctx) error {
	if err := h.recs.Archive(c.Context(), middleware.GetSpace(c), c.Params("id"), h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// occurrenceHistoryLimit is how many made bills F4's detail shows: a year of a
// monthly rule, which is what a person checks ("did I pay the last ones?").
const occurrenceHistoryLimit = 12

// occurrenceUpcomingCount is how many dates still to be made it shows.
const occurrenceUpcomingCount = 6

type occurrenceBillDTO struct {
	Nominal        brcal.Date    `json:"nominal"`
	Due            brcal.Date    `json:"due"`
	BillID         string        `json:"bill_id,omitempty"`
	Amount         billing.Cents `json:"amount"`
	ExpectedAmount billing.Cents `json:"expected_amount,omitempty"`
	// State is paid, forecast, overdue (a forecast past its due date),
	// skipped (the occurrence's bill was cancelled), or posted (a card purchase
	// the job made, on the statement of statement_month).
	State    string      `json:"state"`
	PaidDate *brcal.Date `json:"paid_date,omitempty"`
	// AutoSettle is the bill's own flag (copied when it was made): an open bill
	// that has it is still paid by the daily job after the recurrence ends.
	AutoSettle     bool   `json:"auto_settle"`
	PurchaseID     string `json:"purchase_id,omitempty"`
	CardID         string `json:"card_id,omitempty"`
	StatementMonth string `json:"statement_month,omitempty"`
}

type recurrenceOccurrencesDTO struct {
	History  []occurrenceBillDTO `json:"history"`
	Upcoming []occurrenceDTO     `json:"upcoming"`
}

func occurrenceState(b finance.Bill, today brcal.Date) string {
	switch {
	case b.Status == finance.BillPaid:
		return "paid"
	case b.Status == finance.BillCanceled:
		return "skipped"
	case b.Due.Before(today):
		return "overdue"
	default:
		return "forecast"
	}
}

// recurrenceOccurrences is F4's inline detail (UX batch 3): the latest bills the
// recurrence made, each with its state, and the next dates the rule will make,
// computed on read from after the job's cursor (never before today). An
// archived recurrence has no next dates. Everything is read inside the resolved
// space: another space's id is the ordinary 404.
func (h *financeHandlers) recurrenceOccurrences(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	rec, cursor, err := h.recs.GetWithCursor(c.Context(), sp, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	made, err := h.bills.ForRecurrence(c.Context(), sp, rec.ID, occurrenceHistoryLimit)
	if err != nil {
		return fail(c, err)
	}
	today := h.today()
	out := recurrenceOccurrencesDTO{History: make([]occurrenceBillDTO, 0, len(made)), Upcoming: []occurrenceDTO{}}
	for _, m := range made {
		if m.State == "skipped" {
			out.History = append(out.History, occurrenceBillDTO{Nominal: m.Nominal, Due: m.Nominal, Amount: m.ExpectedAmount, ExpectedAmount: m.ExpectedAmount, State: "skipped"})
			continue
		}
		if p := m.Purchase; p != nil {
			out.History = append(out.History, occurrenceBillDTO{Nominal: m.Nominal, Due: m.Nominal, Amount: p.Amount, State: "posted",
				ExpectedAmount: m.ExpectedAmount, PurchaseID: p.ID, CardID: p.CardID, StatementMonth: p.Statement.String()})
			continue
		}
		d := occurrenceBillDTO{Nominal: m.Nominal, Due: m.Bill.Due, BillID: m.Bill.ID, Amount: m.Bill.Amount, ExpectedAmount: m.ExpectedAmount, State: occurrenceState(m.Bill, today), AutoSettle: m.Bill.AutoSettle}
		if !m.Bill.PaidDate.IsZero() {
			paid := m.Bill.PaidDate
			d.PaidDate = &paid
		}
		out.History = append(out.History, d)
	}
	pending, err := h.recs.ListPendingOccurrencesForRecurrence(c.Context(), sp, rec.ID, occurrenceHistoryLimit)
	if err != nil {
		return fail(c, err)
	}
	for _, item := range pending {
		out.History = append(out.History, occurrenceBillDTO{Nominal: item.Nominal, Due: item.Due, Amount: item.Expected, ExpectedAmount: item.Expected, State: "pending_confirmation"})
	}
	if !rec.Archived {
		from := today
		if !cursor.IsZero() && cursor.AddDays(1).After(from) {
			from = cursor.AddDays(1)
		}
		occ, err := finance.Preview(rec.Schedule, from, occurrenceUpcomingCount)
		if err != nil {
			return fail(c, err)
		}
		var card *repositories.CardRow
		if rec.IsCard() {
			if card, err = h.cards.GetCard(c.Context(), sp, rec.CardID); err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return fail(c, err)
			}
		}
		for _, o := range occ {
			d := occurrenceDTO{Nominal: o.Nominal, Due: o.Due}
			if card != nil {
				d.Due = o.Nominal // a card is charged on the day itself
				if plan, err := finance.PlaceInstallments(rec.Amount, rec.Installments, o.Nominal, card.ClosingDay, card.OpenMonth); err == nil {
					d.StatementMonth = plan[0].Statement.String()
				}
			}
			out.Upcoming = append(out.Upcoming, d)
		}
	}
	return c.JSON(out)
}

type previewRequest struct {
	scheduleRequest
	From  *brcal.Date `json:"from"`
	Count int         `json:"count"`
}

type occurrenceDTO struct {
	Nominal brcal.Date `json:"nominal"`
	Due     brcal.Date `json:"due"`
	// StatementMonth is the statement a card recurrence's occurrence lands on.
	StatementMonth string `json:"statement_month,omitempty"`
}

// previewRecurrence shows the next occurrences of an expression before anything
// is saved (F4). It is stateless: nothing is read from or written to the space's
// tables, which is why it needs only the read verb and no idempotency key.
func (h *financeHandlers) previewRecurrence(c fiber.Ctx) error {
	var req previewRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	sched, errs := req.scheduleRequest.schedule(h.today())
	if req.From != nil && !req.From.IsZero() {
		ch := &checks{}
		ch.date("from", *req.From, limits.MinDate, h.today().AddYears(limits.MaxRecurrenceYears))
		errs = append(errs, ch.errs...)
	}
	if req.Count < 1 || req.Count > finance.MaxPreview {
		errs = append(errs, fieldErr("count", "out_of_range", fmt.Sprintf("between 1 and %d", finance.MaxPreview), "min", 1, "max", finance.MaxPreview))
	}
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	from := h.today()
	if req.From != nil && !req.From.IsZero() {
		from = *req.From
	}
	occ, err := finance.Preview(sched, from, req.Count)
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[occurrenceDTO]{Data: make([]occurrenceDTO, 0, len(occ))}
	for _, o := range occ {
		out.Data = append(out.Data, occurrenceDTO{Nominal: o.Nominal, Due: o.Due})
	}
	return c.JSON(out)
}

type projectionMonthDTO struct {
	Month      string        `json:"month"`
	Receivable billing.Cents `json:"receivable"`
	Payable    billing.Cents `json:"payable"`
	Virtual    billing.Cents `json:"virtual"`
	// VirtualReceivable and VirtualPayable split Virtual into its two sides (both
	// >= 0), so a client can show money in and money out separately.
	VirtualReceivable billing.Cents `json:"virtual_receivable"`
	VirtualPayable    billing.Cents `json:"virtual_payable"`
}

func (h *financeHandlers) projection(c fiber.Ctx) error {
	months := fiber.Query(c, "months", 6)
	if months < 1 || months > 12 {
		return problem.Validation([]problem.FieldError{fieldErr("months", "out_of_range", "between 1 and 12", "min", 1, "max", 12)}).Send(c)
	}
	p, err := h.jobs.Project(c.Context(), middleware.GetSpace(c), h.today(), months)
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[projectionMonthDTO]{Data: make([]projectionMonthDTO, 0, len(p.Months))}
	for _, m := range p.Months {
		out.Data = append(out.Data, projectionMonthDTO{Month: m.Month.String(), Receivable: m.Receivable, Payable: m.Payable, Virtual: m.Virtual, VirtualReceivable: m.VirtualReceivable, VirtualPayable: m.VirtualPayable})
	}
	return c.JSON(out)
}

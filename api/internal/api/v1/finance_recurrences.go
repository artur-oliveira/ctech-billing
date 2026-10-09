package v1

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type recurrenceDTO struct {
	ID          string          `json:"id"`
	Direction   string          `json:"direction"`
	Amount      billing.Cents   `json:"amount"`
	CategoryID  string          `json:"category_id"`
	AccountID   string          `json:"account_id"`
	Description string          `json:"description,omitempty"`
	Expression  json.RawMessage `json:"expression"`
	Start       brcal.Date      `json:"start"`
	End         *brcal.Date     `json:"end,omitempty"`
	Adjust      string          `json:"business_day_adjust"`
	AutoSettle  bool            `json:"auto_settle"`
	Archived    bool            `json:"archived"`
}

func newRecurrenceDTO(r finance.Recurrence) (recurrenceDTO, error) {
	expr, err := finance.MarshalExpression(r.Schedule.Expression)
	if err != nil {
		return recurrenceDTO{}, err
	}
	d := recurrenceDTO{
		ID: r.ID, Direction: string(r.Direction), Amount: r.Amount, CategoryID: r.CategoryID, AccountID: r.AccountID,
		Description: r.Description, Expression: expr, Start: r.Schedule.Start, Adjust: string(r.Schedule.Adjust),
		AutoSettle: r.AutoSettle, Archived: r.Archived,
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
	Direction   string        `json:"direction"`
	Amount      billing.Cents `json:"amount"`
	CategoryID  string        `json:"category_id"`
	AccountID   string        `json:"account_id"`
	Description string        `json:"description"`
	AutoSettle  bool          `json:"auto_settle"`
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
	ch := &checks{}
	ch.amount("amount", req.Amount)
	ch.id("category_id", req.CategoryID, true)
	ch.id("account_id", req.AccountID, true)
	ch.text("description", req.Description, false, limits.Description)
	errs = append(errs, ch.errs...)
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	rec, err := h.recs.Create(c.Context(), middleware.GetSpace(c), finance.Recurrence{
		Direction: finance.Direction(req.Direction), Amount: req.Amount, CategoryID: req.CategoryID,
		AccountID: req.AccountID, Description: req.Description, Schedule: sched, AutoSettle: req.AutoSettle,
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

type patchRecurrenceRequest struct {
	Amount      *billing.Cents `json:"amount"`
	CategoryID  *string        `json:"category_id"`
	AccountID   *string        `json:"account_id"`
	Description *string        `json:"description"`
	AutoSettle  *bool          `json:"auto_settle"`
	End         *brcal.Date    `json:"end"`
	// Archive confirms an end that leaves nothing to come: the edit and the
	// archive are one write. Without it such an end is 422 recurrence_would_end.
	Archive bool `json:"archive"`
}

func (h *financeHandlers) patchRecurrence(c fiber.Ctx) error {
	var req patchRecurrenceRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	if req.Amount != nil {
		ch.amount("amount", *req.Amount)
	}
	if req.CategoryID != nil {
		ch.id("category_id", *req.CategoryID, true)
	}
	if req.AccountID != nil {
		ch.id("account_id", *req.AccountID, true)
	}
	if req.Description != nil {
		ch.text("description", *req.Description, false, limits.Description)
	}
	if req.End != nil && !req.End.IsZero() {
		ch.date("end", *req.End, limits.MinDate, h.today().AddYears(limits.MaxRecurrenceYears))
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	sp := middleware.GetSpace(c)
	if err := h.recs.Update(c.Context(), sp, c.Params("id"), repositories.RecurrencePatch{
		Amount: req.Amount, CategoryID: req.CategoryID, AccountID: req.AccountID,
		Description: req.Description, AutoSettle: req.AutoSettle, End: req.End, Archive: req.Archive,
	}, h.now()); err != nil {
		return fail(c, err)
	}
	rec, err := h.recs.Get(c.Context(), sp, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	dto, err := newRecurrenceDTO(*rec)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(dto)
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
	Nominal brcal.Date    `json:"nominal"`
	Due     brcal.Date    `json:"due"`
	BillID  string        `json:"bill_id"`
	Amount  billing.Cents `json:"amount"`
	// State is paid, forecast, overdue (a forecast past its due date) or
	// skipped (the occurrence's bill was cancelled).
	State    string      `json:"state"`
	PaidDate *brcal.Date `json:"paid_date,omitempty"`
	// AutoSettle is the bill's own flag (copied when it was made): an open bill
	// that has it is still paid by the daily job after the recurrence ends.
	AutoSettle bool `json:"auto_settle"`
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
		d := occurrenceBillDTO{Nominal: m.Nominal, Due: m.Bill.Due, BillID: m.Bill.ID, Amount: m.Bill.Amount, State: occurrenceState(m.Bill, today), AutoSettle: m.Bill.AutoSettle}
		if !m.Bill.PaidDate.IsZero() {
			paid := m.Bill.PaidDate
			d.PaidDate = &paid
		}
		out.History = append(out.History, d)
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
		for _, o := range occ {
			out.Upcoming = append(out.Upcoming, occurrenceDTO{Nominal: o.Nominal, Due: o.Due})
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

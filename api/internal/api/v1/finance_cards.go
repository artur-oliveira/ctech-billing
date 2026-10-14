package v1

import (
	"strings"

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

type cardDTO struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	ClosingDay      int           `json:"closing_day"`
	DueDay          int           `json:"due_day"`
	PayingAccountID string        `json:"paying_account_id"`
	OpenMonth       string        `json:"open_month"`
	Balance         billing.Cents `json:"balance"` // the card account's: negative is owed
	Archived        bool          `json:"archived"`
	Brand           string        `json:"brand,omitempty"`
	Last4           string        `json:"last4,omitempty"`
}

func newCardDTO(c repositories.CardRow) cardDTO {
	return cardDTO{ID: c.ID, Name: c.Name, ClosingDay: c.ClosingDay, DueDay: c.DueDay, PayingAccountID: c.PayingAccountID,
		OpenMonth: c.OpenMonth.String(), Balance: billing.Cents(c.Balance), Archived: c.Archived,
		Brand: string(c.Brand), Last4: c.Last4}
}

type statementItemDTO struct {
	PurchaseID   string        `json:"purchase_id"`
	Description  string        `json:"description"`
	CategoryID   string        `json:"category_id,omitempty"`
	Date         string        `json:"date"`
	Number       int           `json:"number,omitempty"`
	Of           int           `json:"of,omitempty"`
	Kind         string        `json:"kind"`
	Amount       billing.Cents `json:"amount"`
	RecurrenceID string        `json:"recurrence_id,omitempty"`
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
	// Origin is "recurrence" on a purchase the daily job posted for RecurrenceID.
	Origin       string `json:"origin,omitempty"`
	RecurrenceID string `json:"recurrence_id,omitempty"`
}

func newPurchaseDTO(p repositories.Purchase) purchaseDTO {
	out := purchaseDTO{ID: p.ID, Description: p.Description, CategoryID: p.CategoryID, Date: p.Date.String(),
		Total: p.Total, Refunded: p.Refunded, Installments: make([]installmentDTO, len(p.Installments)), RecurrenceID: p.RecurrenceID}
	if p.RecurrenceID != "" {
		out.Origin = "recurrence"
	}
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
			Date: it.Date.String(), Number: it.Number, Of: it.Of, Kind: it.Kind, Amount: it.Amount,
			RecurrenceID: it.RecurrenceID}
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
	Brand           string `json:"brand"`
	Last4           string `json:"last4"`
}

// cardIdentity checks a card's brand and last four digits, both optional.
func cardIdentity(c *checks, brand, last4 string) {
	if brand != "" && !finance.ValidCardBrand(finance.CardBrand(brand)) {
		allowed := make([]string, len(finance.CardBrands))
		for i, b := range finance.CardBrands {
			allowed[i] = string(b)
		}
		c.fail("brand", "unsupported_value", "one of the listed brands", "allowed", allowed)
	}
	if last4 != "" && !finance.ValidLast4(last4) {
		c.fail("last4", "invalid_format", "exactly four digits", "format", "0000")
	}
}

func (r cardRequest) validate() []problem.FieldError {
	c := &checks{}
	c.text("name", r.Name, true, limits.AccountName)
	if r.ClosingDay < 1 || r.ClosingDay > 31 {
		c.fail("closing_day", "out_of_range", "a day between 1 and 31", "min", 1, "max", 31)
	}
	if r.DueDay < 1 || r.DueDay > 31 {
		c.fail("due_day", "out_of_range", "a day between 1 and 31", "min", 1, "max", 31)
	}
	c.id("paying_account_id", r.PayingAccountID, true)
	cardIdentity(c, r.Brand, r.Last4)
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
		finance.Card{ClosingDay: req.ClosingDay, DueDay: req.DueDay, PayingAccountID: req.PayingAccountID,
			Brand: finance.CardBrand(req.Brand), Last4: req.Last4}, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(newCardDTO(row))
}

type cardPatchRequest struct {
	// Name renames the card (its account): absent keeps, null or blank is a 422.
	Name            patch.Optional[string] `json:"name"`
	ClosingDay      patch.Optional[int]    `json:"closing_day"`
	DueDay          patch.Optional[int]    `json:"due_day"`
	PayingAccountID patch.Optional[string] `json:"paying_account_id"`
	// Brand and Last4: absent keeps, null clears ("" also clears, as in batch 3).
	Brand patch.Optional[string] `json:"brand"`
	Last4 patch.Optional[string] `json:"last4"`
}

// edit validates the body under the PATCH rule (package patch).
func (r cardPatchRequest) edit() (repositories.CardPatch, []problem.FieldError) {
	ch := &checks{}
	var name *string
	if v, ok := required(ch, "name", r.Name); ok {
		name = renameField(ch, v)
	}
	day := func(field string, o patch.Optional[int]) {
		if v, ok := required(ch, field, o); ok && (v < 1 || v > 31) {
			ch.fail(field, "out_of_range", "a day between 1 and 31", "min", 1, "max", 31)
		}
	}
	day("closing_day", r.ClosingDay)
	day("due_day", r.DueDay)
	if v, ok := required(ch, "paying_account_id", r.PayingAccountID); ok {
		ch.id("paying_account_id", v, true)
	}
	brand, last4 := clearable(r.Brand), clearable(r.Last4)
	b, l := "", ""
	if brand != nil {
		b = *brand
	}
	if last4 != nil {
		l = *last4
	}
	cardIdentity(ch, b, l)
	return repositories.CardPatch{
		Name:       name,
		ClosingDay: r.ClosingDay.Ptr(), DueDay: r.DueDay.Ptr(), PayingAccountID: r.PayingAccountID.Ptr(), Brand: brand, Last4: last4,
	}, ch.errs
}

func (h *financeHandlers) patchCard(c fiber.Ctx) error {
	var req cardPatchRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	edit, errs := req.edit()
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	row, err := h.cards.UpdateCard(c.Context(), middleware.GetSpace(c), c.Params("id"), edit, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newCardDTO(row))
}

func (h *financeHandlers) cardStatement(c fiber.Ctx) error {
	m, err := finance.ParseMonth(c.Params("month"))
	if err != nil {
		return problem.Validation([]problem.FieldError{fieldErr("month", "invalid_format", "use YYYY-MM", "format", "YYYY-MM")}).Send(c)
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

type cardForecastDTO struct {
	RecurrenceID        string        `json:"recurrence_id"`
	Description         string        `json:"description,omitempty"`
	Nominal             brcal.Date    `json:"nominal"`
	Installment         int           `json:"installment"`
	Of                  int           `json:"of"`
	Amount              billing.Cents `json:"amount"`
	PendingConfirmation bool          `json:"pending_confirmation,omitempty"`
}

// cardForecast is what a card's recurrences will add to its statements, by
// statement month: the occurrences the daily job has not yet made a purchase
// (after the recurrence's cursor, from tomorrow on), each placed in its
// installments, and the occurrences awaiting confirmation, at their expected
// amount, marked as such. One pass over the recurrences serves every month asked
// for, so the timeline and a single month are the same arithmetic.
func cardForecast(cardID string, closingDay int, open finance.Month, recurrences []repositories.DueRecurrence, pending []repositories.PendingOccurrence, today brcal.Date, months []finance.Month) (map[finance.Month][]cardForecastDTO, error) {
	wanted := make(map[finance.Month]bool, len(months))
	last := months[0]
	for _, m := range months {
		wanted[m] = true
		if m.Compare(last) > 0 {
			last = m
		}
	}
	out := make(map[finance.Month][]cardForecastDTO, len(months))
	from := today.AddDays(1)
	to := last.Add(1).First()
	for _, entry := range recurrences {
		rec := entry.Recurrence
		if rec.Archived || !rec.IsCard() || rec.CardID != cardID || !from.Before(to) {
			continue
		}
		start := from
		if rec.Schedule.Start.After(start) {
			start = rec.Schedule.Start
		}
		if !start.Before(to) {
			continue
		}
		days, err := rec.Schedule.Occurrences(start, to)
		if err != nil {
			return nil, err
		}
		for _, occurrence := range days {
			if !entry.Cursor.IsZero() && !occurrence.Nominal.After(entry.Cursor) {
				continue
			}
			plan, err := finance.PlaceInstallments(rec.Amount, rec.Installments, occurrence.Nominal, closingDay, open)
			if err != nil {
				return nil, err
			}
			for _, installment := range plan {
				if wanted[installment.Statement] {
					out[installment.Statement] = append(out[installment.Statement], cardForecastDTO{RecurrenceID: rec.ID, Description: rec.Description, Nominal: occurrence.Nominal,
						Installment: installment.Number, Of: len(plan), Amount: installment.Amount})
				}
			}
		}
	}
	for _, occurrence := range pending {
		if occurrence.CardID != cardID {
			continue
		}
		plan, err := finance.PlaceInstallments(occurrence.Expected, occurrence.Installments, occurrence.Nominal, closingDay, open)
		if err != nil {
			return nil, err
		}
		for _, installment := range plan {
			if wanted[installment.Statement] {
				out[installment.Statement] = append(out[installment.Statement], cardForecastDTO{RecurrenceID: occurrence.RecurrenceID, Description: occurrence.Description, Nominal: occurrence.Nominal,
					Installment: installment.Number, Of: len(plan), Amount: installment.Amount, PendingConfirmation: true})
			}
		}
	}
	return out, nil
}

// cardForecastFor reads the recurrences and pending occurrences once and
// forecasts `months` for the card.
func (h *financeHandlers) cardForecastFor(c fiber.Ctx, card *repositories.CardRow, months []finance.Month) (map[finance.Month][]cardForecastDTO, error) {
	sp := middleware.GetSpace(c)
	recurrences, err := h.recs.ListWithCursors(c.Context(), sp)
	if err != nil {
		return nil, err
	}
	pending, err := h.recs.ListPendingOccurrences(c.Context(), sp, 5000)
	if err != nil {
		return nil, err
	}
	return cardForecast(card.ID, card.ClosingDay, card.OpenMonth, recurrences, pending, h.today(), months)
}

func (h *financeHandlers) cardStatementForecast(c fiber.Ctx) error {
	month, err := finance.ParseMonth(c.Params("month"))
	if err != nil {
		return problem.Validation([]problem.FieldError{fieldErr("month", "invalid_format", "use YYYY-MM", "format", "YYYY-MM")}).Send(c)
	}
	card, err := h.cards.GetCard(c.Context(), middleware.GetSpace(c), c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	if card.Archived {
		return fail(c, repositories.ErrNotFound)
	}
	byMonth, err := h.cardForecastFor(c, card, []finance.Month{month})
	if err != nil {
		return fail(c, err)
	}
	data := byMonth[month]
	if data == nil {
		data = make([]cardForecastDTO, 0)
	}
	return c.JSON(listResponse[cardForecastDTO]{Data: data})
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
	installmentChecks(c, r.Total, r.Installments)
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

// purchasePatchRequest edits a purchase's safe fields; anything else (total,
// date, installments) is an unknown field, refused by decodeStrict.
type purchasePatchRequest struct {
	Description patch.Optional[string] `json:"description"`
	CategoryID  patch.Optional[string] `json:"category_id"`
}

// edit validates like a purchase's creation: neither can be cleared.
func (r purchasePatchRequest) edit() (repositories.PurchaseEdit, []problem.FieldError) {
	ch := &checks{}
	var out repositories.PurchaseEdit
	if v, ok := required(ch, "description", r.Description); ok {
		ch.text("description", v, true, limits.Description)
		d := strings.TrimSpace(v)
		out.Description = &d
	}
	if v, ok := required(ch, "category_id", r.CategoryID); ok {
		ch.id("category_id", v, true)
		out.CategoryID = &v
	}
	return out, ch.errs
}

func (h *financeHandlers) patchPurchase(c fiber.Ctx) error {
	var req purchasePatchRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	edit, errs := req.edit()
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	p, err := h.cards.EditPurchase(c.Context(), middleware.GetSpace(c), c.Params("id"), c.Params("pid"), edit, h.postMeta(c, ""), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newPurchaseDTO(p))
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
		return problem.Validation([]problem.FieldError{fieldErr("month", "invalid_format", "use YYYY-MM", "format", "YYYY-MM")}).Send(c)
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

type statementSummaryDTO struct {
	CardID      string        `json:"card_id"`
	Month       string        `json:"month"`
	Status      string        `json:"status"` // open | future | closed | paid
	ClosingDate string        `json:"closing_date"`
	DueDate     string        `json:"due_date"`
	Total       billing.Cents `json:"total"`
	BillID      string        `json:"bill_id,omitempty"`
	ItemsCount  int           `json:"items_count"`
	// ForecastTotal is what the card's recurrences will still add to this
	// statement (not in Total until they are purchases), and ForecastEstimated
	// says some of it is an expected amount awaiting confirmation.
	ForecastTotal     billing.Cents `json:"forecast_total"`
	ForecastEstimated bool          `json:"forecast_estimated,omitempty"`
}

// forecastTotals sums a month's forecast and reports whether any part of it is an estimate.
func forecastTotals(items []cardForecastDTO) (total billing.Cents, estimated bool) {
	for _, item := range items {
		total += item.Amount
		estimated = estimated || item.PendingConfirmation
	}
	return total, estimated
}

// cardStatements is the timeline's read (spec § 4.1): summaries only; the
// selected month's items come from GET /cards/:id/statements/:month.
func (h *financeHandlers) cardStatements(c fiber.Ctx) error {
	ch := &checks{}
	from, err := finance.ParseMonth(c.Query("from"))
	if err != nil {
		ch.fail("from", "invalid_format", "use YYYY-MM", "format", "YYYY-MM")
	}
	count := fiber.Query(c, "count", 3)
	if count < 1 || count > repositories.MaxStatementPage {
		ch.fail("count", "out_of_range", "between 1 and 12", "min", 1, "max", repositories.MaxStatementPage)
	}
	direction := c.Query("direction", "forward")
	if direction != "forward" && direction != "backward" {
		ch.fail("direction", "unsupported_value", "use forward or backward", "allowed", []string{"forward", "backward"})
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	sp := middleware.GetSpace(c)
	page, err := h.cards.StatementRange(c.Context(), sp, c.Params("id"), from, count, direction == "backward")
	if err != nil {
		return fail(c, err)
	}
	var billIDs []string
	for _, s := range page.Statements {
		if s.BillID != "" {
			billIDs = append(billIDs, s.BillID)
		}
	}
	paid, err := h.bills.PaidAmong(c.Context(), sp, billIDs)
	if err != nil {
		return fail(c, err)
	}
	forecast := map[finance.Month][]cardForecastDTO{}
	if len(page.Statements) > 0 {
		card, err := h.cards.GetCard(c.Context(), sp, c.Params("id"))
		if err != nil {
			return fail(c, err)
		}
		if !card.Archived {
			months := make([]finance.Month, len(page.Statements))
			for i, s := range page.Statements {
				months[i] = s.Month
			}
			if forecast, err = h.cardForecastFor(c, card, months); err != nil {
				return fail(c, err)
			}
		}
	}
	out := listResponse[statementSummaryDTO]{Data: make([]statementSummaryDTO, 0, len(page.Statements)), HasMore: page.HasMore}
	for _, s := range page.Statements {
		status := s.Status
		if s.BillID != "" && paid[s.BillID] {
			status = "paid"
		}
		forecastTotal, forecastEstimated := forecastTotals(forecast[s.Month])
		out.Data = append(out.Data, statementSummaryDTO{CardID: s.CardID, Month: s.Month.String(), Status: status,
			ClosingDate: s.ClosingDate.String(), DueDate: s.DueDate.String(), Total: s.Total, BillID: s.BillID, ItemsCount: len(s.Items),
			ForecastTotal: forecastTotal, ForecastEstimated: forecastEstimated})
	}
	return c.JSON(out)
}

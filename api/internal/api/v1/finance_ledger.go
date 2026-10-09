package v1

import (
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

const maxMemo = 140

type entryDTO struct {
	TransactionID string        `json:"transaction_id"`
	Date          string        `json:"date"`
	Amount        billing.Cents `json:"amount"` // signed, positive = money in
	Balance       billing.Cents `json:"balance"`
	Kind          string        `json:"kind"` // "" for entries posted before 6.4
	Memo          string        `json:"memo"`
	CategoryID    string        `json:"category_id,omitempty"`
	BillID        string        `json:"bill_id,omitempty"`
	Reversal      bool          `json:"reversal"`
	Reversed      bool          `json:"reversed"`
}

type statementDTO struct {
	AccountID string        `json:"account_id"`
	From      string        `json:"from"`
	To        string        `json:"to"`
	Opening   billing.Cents `json:"opening"`
	Closing   billing.Cents `json:"closing"`
	Entries   []entryDTO    `json:"entries"`
}

// statement is one cash account's lines for a period with its running balance.
// A category or a system account has no statement: 404, like an unknown id.
func (h *financeHandlers) statement(c fiber.Ctx) error {
	from, to, err := parseDateRange(c.Query("from"), c.Query("to"))
	if err != nil {
		return problem.Validation([]problem.FieldError{fieldErr("from", err.Error(), "range")}).Send(c)
	}
	sp, id := middleware.GetSpace(c), c.Params("id")
	acct, err := h.ledger.GetAccount(c.Context(), sp, id)
	if err != nil {
		return fail(c, err)
	}
	if acct.Class != finance.ClassAsset || acct.System {
		return fail(c, repositories.ErrNotFound)
	}
	es, err := h.ledger.EntriesFrom(c.Context(), sp, id, from)
	if err != nil {
		return fail(c, err)
	}
	s := finance.BuildStatement(acct.Balance, es, from, to)
	out := statementDTO{AccountID: id, From: from.String(), To: to.String(), Opening: s.Opening, Closing: s.Closing, Entries: make([]entryDTO, len(s.Lines))}
	for i, l := range s.Lines {
		d := entryDTO{
			TransactionID: l.TxID, Date: l.Date.String(), Amount: l.Amount, Balance: l.Balance,
			Kind: string(l.Kind), Memo: l.Memo, Reversal: l.Reversal, Reversed: l.Reversed,
		}
		if l.Flow != "" && l.Flow != finance.FlowNone {
			d.CategoryID = l.Flow
		}
		if bill, ok := strings.CutPrefix(l.Ref, "bill:"); ok {
			d.BillID = bill
		}
		out.Entries[i] = d
	}
	return c.JSON(out)
}

type transactionCreatedDTO struct {
	TransactionID string `json:"transaction_id"`
}

func (h *financeHandlers) postMeta(c fiber.Ctx, memo string) repositories.PostMeta {
	return repositories.PostMeta{
		Origin: "manual", Actor: actorOfUser(c), RequestID: middleware.GetRequestID(c), Memo: memo,
		IdempotencyKey: c.Get(middleware.IdempotencyHeader),
	}
}

// parseDay reads a YYYY-MM-DD request field; the field error names it.
func parseDay(field, s string) (brcal.Date, *problem.FieldError) {
	d, err := brcal.Parse(s)
	if err != nil {
		e := fieldErr(field, "use YYYY-MM-DD", "date")
		return d, &e
	}
	return d, nil
}

type openingBalanceRequest struct {
	Amount billing.Cents `json:"amount"`
	Date   string        `json:"date"`
}

// openingBalance records what an account held when the ledger started. The
// amount may be negative (an overdrawn account) but not zero.
func (h *financeHandlers) openingBalance(c fiber.Ctx) error {
	var req openingBalanceRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	var errs []problem.FieldError
	if req.Amount == 0 {
		errs = append(errs, fieldErr("amount", "informe um valor diferente de zero", "ne"))
	}
	date, e := parseDay("date", req.Date)
	if e != nil {
		errs = append(errs, *e)
	}
	if len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	txID, err := h.ledger.PostOpeningBalance(c.Context(), middleware.GetSpace(c), c.Params("id"), req.Amount, date, h.postMeta(c, ""), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(transactionCreatedDTO{TransactionID: txID})
}

type transferRequest struct {
	FromAccountID string        `json:"from_account_id"`
	ToAccountID   string        `json:"to_account_id"`
	Amount        billing.Cents `json:"amount"`
	Date          string        `json:"date"`
	Memo          string        `json:"memo"`
}

func (r transferRequest) validate() []problem.FieldError {
	var errs []problem.FieldError
	if r.FromAccountID == "" {
		errs = append(errs, fieldErr("from_account_id", "obrigatório", "required"))
	}
	switch {
	case r.ToAccountID == "":
		errs = append(errs, fieldErr("to_account_id", "obrigatório", "required"))
	case r.ToAccountID == r.FromAccountID:
		errs = append(errs, fieldErr("to_account_id", "escolha uma conta diferente da de origem", "nefield"))
	}
	if r.Amount <= 0 {
		errs = append(errs, fieldErr("amount", "informe um valor em centavos maior que zero", "gt"))
	}
	if _, e := parseDay("date", r.Date); e != nil {
		errs = append(errs, *e)
	}
	if utf8.RuneCountInString(r.Memo) > maxMemo {
		errs = append(errs, fieldErr("memo", "no máximo 140 caracteres", "max"))
	}
	return errs
}

func (h *financeHandlers) transfer(c fiber.Ctx) error {
	var req transferRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if errs := req.validate(); len(errs) > 0 {
		return problem.Validation(errs).Send(c)
	}
	date, _ := brcal.Parse(req.Date)
	txID, err := h.ledger.PostTransfer(c.Context(), middleware.GetSpace(c), req.FromAccountID, req.ToAccountID, req.Amount, date,
		h.postMeta(c, strings.TrimSpace(req.Memo)), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(transactionCreatedDTO{TransactionID: txID})
}

// reverse takes back a transfer or an opening balance, dated today.
func (h *financeHandlers) reverse(c fiber.Ctx) error {
	txID, err := h.ledger.ReverseManual(c.Context(), middleware.GetSpace(c), c.Params("id"), h.today(), h.postMeta(c, ""), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(transactionCreatedDTO{TransactionID: txID})
}

// unsettleBill undoes a payment and returns the bill to the open list.
func (h *financeHandlers) unsettleBill(c fiber.Ctx) error {
	b, err := h.bills.Unsettle(c.Context(), middleware.GetSpace(c), c.Params("id"), actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newBillDTO(b, h.today()))
}

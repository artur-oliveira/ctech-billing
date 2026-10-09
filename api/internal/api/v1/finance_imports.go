package v1

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type rejectedDTO struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

type importDTO struct {
	ID            string        `json:"id,omitempty"`
	AccountID     string        `json:"account_id"`
	Format        string        `json:"format"`
	CreatedAt     string        `json:"created_at"`
	From          *brcal.Date   `json:"from,omitempty"`
	To            *brcal.Date   `json:"to,omitempty"`
	Lines         int           `json:"lines"`
	Duplicates    int           `json:"duplicates"`
	RejectedCount int           `json:"rejected_count"`
	Rejected      []rejectedDTO `json:"rejected"`
	Pending       int           `json:"pending"`
}

func newImportDTO(i repositories.Import) importDTO {
	out := importDTO{ID: i.ID, AccountID: i.AccountID, Format: string(i.Format), CreatedAt: i.CreatedAt.UTC().Format(time.RFC3339),
		Lines: i.Lines, Duplicates: i.Duplicates, RejectedCount: i.RejectedCount, Pending: i.Pending(), Rejected: make([]rejectedDTO, len(i.Rejected))}
	if !i.From.IsZero() {
		from, to := i.From, i.To
		out.From, out.To = &from, &to
	}
	for k, r := range i.Rejected {
		out.Rejected[k] = rejectedDTO{Line: r.Line, Reason: r.Reason}
	}
	return out
}

type importLineDTO struct {
	N           int           `json:"n"`
	Date        brcal.Date    `json:"date"`
	Amount      billing.Cents `json:"amount"`
	Description string        `json:"description"`
	Status      string        `json:"status"`
	BillID      string        `json:"bill_id,omitempty"`
	Candidates  []billDTO     `json:"candidates"`
}

func newImportLineDTO(l repositories.ImportLine, candidates []billDTO) importLineDTO {
	if candidates == nil {
		candidates = []billDTO{}
	}
	return importLineDTO{N: l.N, Date: l.Date, Amount: l.Amount, Description: l.Description, Status: string(l.Status), BillID: l.BillID, Candidates: candidates}
}

type importDetailDTO struct {
	Import importDTO       `json:"import"`
	Lines  []importLineDTO `json:"lines"`
}

func (h *financeHandlers) listImports(c fiber.Ctx) error {
	ch := &checks{}
	ch.id("account_id", c.Query("account_id"), false)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	rows, err := h.imports.List(c.Context(), middleware.GetSpace(c), c.Query("account_id"), h.now())
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[importDTO]{Data: make([]importDTO, len(rows))}
	for i, r := range rows {
		out.Data[i] = newImportDTO(r)
	}
	return c.JSON(out)
}

// uploadRequest carries the file as base64 inside JSON: the bytes reach the
// parser exactly as the bank wrote them (a browser reading the file as text
// would decode a Windows-1252 export as UTF-8 and mangle it), and the body —
// which the idempotency layer hashes — names the account and the format too.
type uploadRequest struct {
	AccountID string `json:"account_id"`
	Format    string `json:"format"`
	Content   string `json:"content"`
}

func (h *financeHandlers) uploadImport(c fiber.Ctx) error {
	var req uploadRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.id("account_id", req.AccountID, true)
	if req.Format != string(statement.FormatOFX) && req.Format != string(statement.FormatCSV) {
		ch.fail("format", "unsupported_value", "use ofx or csv", "allowed", []string{"ofx", "csv"})
	}
	file, err := base64.StdEncoding.DecodeString(req.Content)
	switch {
	case req.Content == "":
		ch.fail("content", "required", "required")
	case err != nil:
		ch.fail("content", "invalid_format", "the file must be base64")
	case len(file) > statement.MaxFileBytes:
		ch.fail("content", "too_large", "the file is larger than 1 MiB", "max", statement.MaxFileBytes)
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	imp, err := h.imports.Upload(c.Context(), middleware.GetSpace(c), req.AccountID, statement.Format(req.Format), file,
		c.Get(middleware.IdempotencyHeader), h.now())
	if err != nil {
		return fail(c, err)
	}
	status := fiber.StatusCreated
	if imp.ID == "" {
		status = fiber.StatusOK // nothing new: no import was created
	}
	return c.Status(status).JSON(newImportDTO(imp))
}

func (h *financeHandlers) getImport(c fiber.Ctx) error {
	imp, lines, err := h.imports.Get(c.Context(), middleware.GetSpace(c), c.Params("id"), h.now())
	if err != nil {
		return fail(c, err)
	}
	today := h.today()
	out := importDetailDTO{Import: newImportDTO(imp), Lines: make([]importLineDTO, len(lines))}
	for i, l := range lines {
		cands := make([]billDTO, len(l.Candidates))
		for k, b := range l.Candidates {
			cands[k] = newBillDTO(b, today)
		}
		out.Lines[i] = newImportLineDTO(l.ImportLine, cands)
	}
	return c.JSON(out)
}

// lineParam reads :n, a line's 1-based position.
func lineParam(c fiber.Ctx) (int, *problem.Problem) {
	n, err := strconv.Atoi(c.Params("n"))
	if err != nil || n < 1 || n > statement.MaxLines {
		p := problem.Validation([]problem.FieldError{fieldErr("n", "out_of_range", "a line number", "min", 1, "max", statement.MaxLines)})
		return 0, p
	}
	return n, nil
}

type lineResultDTO struct {
	Line importLineDTO `json:"line"`
	Bill *billDTO      `json:"bill,omitempty"`
}

type matchRequest struct {
	BillID               string `json:"bill_id"`
	DifferenceCategoryID string `json:"difference_category_id"`
}

func (h *financeHandlers) matchLine(c fiber.Ctx) error {
	n, p := lineParam(c)
	if p != nil {
		return p.Send(c)
	}
	var req matchRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.id("bill_id", req.BillID, true)
	ch.id("difference_category_id", req.DifferenceCategoryID, false)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	line, bill, err := h.imports.Match(c.Context(), middleware.GetSpace(c), c.Params("id"), n, req.BillID, req.DifferenceCategoryID,
		actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	b := newBillDTO(bill, h.today())
	return c.JSON(lineResultDTO{Line: newImportLineDTO(line, nil), Bill: &b})
}

type newFromLineRequest struct {
	CategoryID  string `json:"category_id"`
	Description string `json:"description"`
}

func (h *financeHandlers) newFromLine(c fiber.Ctx) error {
	n, p := lineParam(c)
	if p != nil {
		return p.Send(c)
	}
	var req newFromLineRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.id("category_id", req.CategoryID, true)
	ch.text("description", req.Description, false, limits.Description)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	line, bill, err := h.imports.Create(c.Context(), middleware.GetSpace(c), c.Params("id"), n, req.CategoryID, req.Description,
		actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	b := newBillDTO(bill, h.today())
	return c.Status(fiber.StatusCreated).JSON(lineResultDTO{Line: newImportLineDTO(line, nil), Bill: &b})
}

func (h *financeHandlers) ignoreLine(c fiber.Ctx) error {
	return h.decideLine(c, h.imports.Ignore)
}

func (h *financeHandlers) reopenLine(c fiber.Ctx) error {
	return h.decideLine(c, h.imports.Reopen)
}

func (h *financeHandlers) decideLine(c fiber.Ctx, run func(context.Context, space.ResolvedSpace, string, int, time.Time) (repositories.ImportLine, error)) error {
	n, p := lineParam(c)
	if p != nil {
		return p.Send(c)
	}
	line, err := run(c.Context(), middleware.GetSpace(c), c.Params("id"), n, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(lineResultDTO{Line: newImportLineDTO(line, nil)})
}

type csvMappingDTO struct {
	Delimiter   string `json:"delimiter"`
	Decimal     string `json:"decimal"`
	DateFormat  string `json:"date_format"`
	SkipRows    int    `json:"skip_rows"`
	Date        int    `json:"date_column"`
	Description int    `json:"description_column"`
	Amount      int    `json:"amount_column"`
	Debit       int    `json:"debit_column"`
}

func (d csvMappingDTO) mapping() statement.Mapping {
	return statement.Mapping{Delimiter: d.Delimiter, Decimal: d.Decimal, DateFormat: d.DateFormat, SkipRows: d.SkipRows,
		Date: d.Date, Description: d.Description, Amount: d.Amount, Debit: d.Debit}
}

func newCSVMappingDTO(m statement.Mapping) csvMappingDTO {
	return csvMappingDTO{Delimiter: m.Delimiter, Decimal: m.Decimal, DateFormat: m.DateFormat, SkipRows: m.SkipRows,
		Date: m.Date, Description: m.Description, Amount: m.Amount, Debit: m.Debit}
}

func (h *financeHandlers) getCSVMapping(c fiber.Ctx) error {
	m, err := h.imports.GetMapping(c.Context(), middleware.GetSpace(c), c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newCSVMappingDTO(m))
}

func (h *financeHandlers) putCSVMapping(c fiber.Ctx) error {
	var req csvMappingDTO
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if err := h.imports.PutMapping(c.Context(), middleware.GetSpace(c), c.Params("id"), req.mapping(), h.now()); err != nil {
		return fail(c, err)
	}
	return c.JSON(req)
}

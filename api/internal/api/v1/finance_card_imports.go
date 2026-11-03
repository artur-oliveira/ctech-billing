package v1

import (
	"encoding/base64"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// bankInter is the only bank whose card statement can be imported so far.
const bankInter = "inter"

func newCardImportDTO(i repositories.Import) importDTO { return newImportDTO(i) }

type cardLineDTO struct {
	N            int           `json:"n"`
	Date         brcal.Date    `json:"date"`
	Amount       billing.Cents `json:"amount"` // the card's side: a purchase is positive, a credit negative
	Description  string        `json:"description"`
	Status       string        `json:"status"`
	PurchaseID   string        `json:"purchase_id,omitempty"` // the purchase it paired with or became
	K            int           `json:"k,omitempty"`           // "Parcela k/of"
	Of           int           `json:"of,omitempty"`
	BankCategory string        `json:"bank_category,omitempty"`
	Payment      bool          `json:"payment,omitempty"` // the previous statement's payment
	ExpiresAt    string        `json:"expires_at,omitempty"`
}

func newCardLineDTO(l repositories.ImportLine) cardLineDTO {
	out := cardLineDTO{N: l.N, Date: l.Date, Amount: l.Amount, Description: l.Description, Status: string(l.Status),
		PurchaseID: l.BillID, K: l.K, Of: l.Of, BankCategory: l.BankCategory, Payment: statement.IsPaymentLine(l.Description)}
	if !l.Expires.IsZero() {
		out.ExpiresAt = l.Expires.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

type suggestionItemDTO struct {
	PurchaseID  string        `json:"purchase_id"`
	Tag         string        `json:"tag"`
	Description string        `json:"description"`
	Amount      billing.Cents `json:"amount"`
	Date        brcal.Date    `json:"date"`
	Number      int           `json:"number"`
	Of          int           `json:"of"`
}

type suggestionDTO struct {
	Kind  string             `json:"kind"` // pair | group | ignore
	Lines []int              `json:"lines"`
	Item  *suggestionItemDTO `json:"item,omitempty"`
}

type cardImportDetailDTO struct {
	Import      importDTO       `json:"import"`
	Lines       []cardLineDTO   `json:"lines"`
	Suggestions []suggestionDTO `json:"suggestions"`
}

type cardUploadRequest struct {
	StatementMonth string `json:"statement_month"`
	Bank           string `json:"bank"`
	Content        string `json:"content"`
}

func (h *financeHandlers) uploadCardImport(c fiber.Ctx) error {
	var req cardUploadRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	month, merr := finance.ParseMonth(req.StatementMonth)
	if merr != nil {
		ch.fail("statement_month", "invalid_format", "use YYYY-MM", "format", "YYYY-MM")
	}
	if req.Bank != bankInter {
		ch.fail("bank", "unsupported_value", "only inter is supported", "allowed", []string{bankInter})
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
	imp, err := h.imports.UploadCard(c.Context(), middleware.GetSpace(c), c.Params("id"), month, file, c.Get(middleware.IdempotencyHeader), h.now())
	if err != nil {
		return fail(c, err)
	}
	status := fiber.StatusCreated
	if imp.ID == "" {
		status = fiber.StatusOK // nothing new: no import was created
	}
	return c.Status(status).JSON(newCardImportDTO(imp))
}

func (h *financeHandlers) getCardImport(c fiber.Ctx) error {
	imp, lines, sugg, err := h.imports.CardSuggestions(c.Context(), middleware.GetSpace(c), c.Params("id"), h.now())
	if err != nil {
		return fail(c, err)
	}
	out := cardImportDetailDTO{Import: newCardImportDTO(imp), Lines: make([]cardLineDTO, len(lines)), Suggestions: make([]suggestionDTO, len(sugg))}
	for i, l := range lines {
		out.Lines[i] = newCardLineDTO(l)
	}
	for i, s := range sugg {
		out.Suggestions[i] = suggestionDTO{Kind: string(s.Kind), Lines: s.Lines}
		if s.Item != nil {
			out.Suggestions[i].Item = &suggestionItemDTO{PurchaseID: s.Item.PurchaseID, Tag: s.Item.Tag, Description: s.Item.Description,
				Amount: s.Item.Amount, Date: s.Item.Date, Number: s.Item.Number, Of: s.Item.Of}
		}
	}
	return c.JSON(out)
}

type confirmRequest struct {
	Lines      []int  `json:"lines"`
	PurchaseID string `json:"purchase_id"`
	Tag        string `json:"tag"`
}

func (h *financeHandlers) confirmCardLines(c fiber.Ctx) error {
	var req confirmRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.id("purchase_id", req.PurchaseID, true)
	ch.text("tag", req.Tag, true, 10)
	if n := len(req.Lines); n < 1 || n > statement.MaxGroupLines {
		ch.fail("lines", "out_of_range", "one to six lines", "min", 1, "max", statement.MaxGroupLines)
	}
	seen := map[int]bool{}
	for _, n := range req.Lines {
		if n < 1 || n > statement.MaxLines || seen[n] {
			ch.fail("lines", "invalid_value", "each a distinct line number")
			break
		}
		seen[n] = true
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	lines, err := h.imports.ConfirmPair(c.Context(), middleware.GetSpace(c), c.Params("id"), req.Lines, req.PurchaseID, req.Tag, h.now())
	if err != nil {
		return fail(c, err)
	}
	out := struct {
		Lines []cardLineDTO `json:"lines"`
	}{Lines: make([]cardLineDTO, len(lines))}
	for i, l := range lines {
		out.Lines[i] = newCardLineDTO(l)
	}
	return c.JSON(out)
}

type confirmAllRequest struct {
	// Exclude names lines the person said are not the purchase offered for them.
	Exclude []int `json:"exclude"`
}

func (h *financeHandlers) confirmAllCardLines(c fiber.Ctx) error {
	var req confirmAllRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	if len(req.Exclude) > statement.MaxLines {
		return problem.Validation([]problem.FieldError{fieldErr("exclude", "out_of_range", "too many lines", "max", statement.MaxLines)}).Send(c)
	}
	exclude := make(map[int]bool, len(req.Exclude))
	for _, n := range req.Exclude {
		exclude[n] = true
	}
	n, err := h.imports.ConfirmAll(c.Context(), middleware.GetSpace(c), c.Params("id"), exclude, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(struct {
		Confirmed int `json:"confirmed"`
	}{n})
}

func (h *financeHandlers) createCardLine(c fiber.Ctx) error {
	n, p := lineParam(c)
	if p != nil {
		return p.Send(c)
	}
	var req newFromLineRequest
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	ch.id("category_id", req.CategoryID, false)
	ch.text("description", req.Description, false, limits.Description)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	line, err := h.imports.CreateFromCardLine(c.Context(), middleware.GetSpace(c), c.Params("id"), n, req.CategoryID, req.Description, h.postMeta(c, ""), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(struct {
		Line cardLineDTO `json:"line"`
	}{newCardLineDTO(line)})
}

type bankCategoriesDTO struct {
	Categories map[string]string `json:"categories"`
}

func (h *financeHandlers) getBankCategories(c fiber.Ctx) error {
	m, err := h.imports.GetBankCategories(c.Context(), middleware.GetSpace(c), c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(bankCategoriesDTO{Categories: m})
}

func (h *financeHandlers) putBankCategories(c fiber.Ctx) error {
	var req bankCategoriesDTO
	if p := decodeStrict(c, &req); p != nil {
		return p.Send(c)
	}
	ch := &checks{}
	for bank, id := range req.Categories {
		ch.id("categories."+bank, id, true)
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	if req.Categories == nil {
		req.Categories = map[string]string{}
	}
	if err := h.imports.PutBankCategories(c.Context(), middleware.GetSpace(c), c.Params("id"), req.Categories, h.now()); err != nil {
		return fail(c, err)
	}
	return c.JSON(req)
}

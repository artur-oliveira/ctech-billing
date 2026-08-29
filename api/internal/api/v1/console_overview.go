package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
)

// C1 — "algo precisa de mim hoje?"
//
// Computed from **one month of invoices**, the same page C2 reads, and nothing
// else. That bound is the design rather than a shortcut: an overview assembled
// from four unbounded queries is a screen whose numbers quietly become wrong at
// the page size nobody tests, and a merchant who trusts a wrong "recebido no
// mês" is worse off than one who opens the list.
//
// So every figure here is explicitly *this month*, the response says how many
// invoices it counted, and `complete` says whether that was all of them. A
// screen that cannot promise a total says so instead of rounding.
type overviewResponse struct {
	Month int `json:"month"`
	Year  int `json:"year"`

	// Received is what was actually collected this month, Open is what is still
	// owed on invoices that are not late, and Overdue is what is owed past the
	// due date. Three amounts, not one "faturado": an operator's first question
	// is which of the three is unusual.
	Received billing.Cents `json:"received"`
	Open     billing.Cents `json:"open"`
	Overdue  billing.Cents `json:"overdue"`

	// Drafts are invoices the sweep created and never finalized — the residue of
	// a half-failed run, which nothing picks up again. It is the one count here
	// that is a defect rather than a state of business, and it exists so C3's
	// finalize is discoverable by the person who needs it.
	Drafts int `json:"drafts"`
	// Uncollectible is the count written off this month, which is the number
	// worth watching over time rather than reacting to today.
	Uncollectible int `json:"uncollectible"`
	// OverdueCount is how many invoices make up Overdue — one large bill and
	// twenty small ones are the same amount and completely different problems.
	OverdueCount int `json:"overdue_count"`

	Counted int `json:"counted"`
	// Complete is false when the month has more invoices than one page. The
	// screen must say so rather than present a partial sum as a total.
	Complete bool `json:"complete"`
}

func (h *consoleHandlers) overview(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	today := h.today()
	year := fiber.Query(c, "year", today.Year)
	month := fiber.Query(c, "month", int(today.Month))
	if month < 1 || month > 12 {
		month = int(today.Month)
	}

	page, err := h.invoices.ListByMonth(c.Context(), t.OrganizationID, t.Livemode, year, month, pageLimit, nil)
	if err != nil {
		return fail(c, err)
	}

	out := overviewResponse{
		Year:     year,
		Month:    month,
		Counted:  len(page.Items),
		Complete: len(page.LastEvaluatedKey) == 0,
	}
	for i := range page.Items {
		inv := &page.Items[i]
		switch inv.Status {
		case billing.InvoicePaid:
			out.Received += inv.AmountPaid
		case billing.InvoiceDraft:
			out.Drafts++
		case billing.InvoiceUncollectible:
			out.Uncollectible++
		case billing.InvoiceOpen:
			if inv.IsOverdue(today) {
				out.Overdue += inv.AmountDue()
				out.OverdueCount++
			} else {
				out.Open += inv.AmountDue()
			}
		}
	}
	return c.JSON(out)
}

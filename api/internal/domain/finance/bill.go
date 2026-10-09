package finance

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrInvalidBill wraps every reason a bill is refused.
var ErrInvalidBill = errors.New("invalid bill")

// ErrBillState is an operation the bill's current status does not allow.
var ErrBillState = errors.New("bill is not in a state that allows this")

type BillStatus string

const (
	BillForecast BillStatus = "forecast"
	BillPaid     BillStatus = "paid"
	BillCanceled BillStatus = "canceled"
)

type BillOrigin string

const (
	OriginManual         BillOrigin = "manual"
	OriginRecurrence     BillOrigin = "recurrence"
	OriginBillingInvoice BillOrigin = "billing_invoice"
	OriginCardStatement  BillOrigin = "card_statement"
	OriginImport         BillOrigin = "import"
)

const maxBillDescription = 200

// Bill is a forecast or recognised obligation (spec § 3.4). Creation posts the
// recognition at the competence date; settlement posts the cash movement; a
// cancellation reverses the recognition.
type Bill struct {
	ID          string
	Direction   Direction
	Amount      billing.Cents
	AccountID   string // the account expected to pay or receive; changeable until settled
	CategoryID  string
	Description string
	Competence  brcal.Date // when the fact belongs (DRE)
	Due         brcal.Date // when it is owed
	PaidDate    brcal.Date // set on settlement (cash)
	Status      BillStatus
	Origin      BillOrigin
	// OriginRef is the recurrence id + nominal date, invoice id, statement id or
	// import line the bill came from. Required for every origin but manual.
	OriginRef string
	// AutoSettle is copied from the recurrence when the bill is materialised, so
	// editing the recurrence later cannot change a bill already on the list.
	AutoSettle     bool
	PaymentGroup   string // the card statement the bill belongs to, when it does
	TransactionIDs []string
}

// Validate refuses a bill the ledger could not post.
func (b Bill) Validate() error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidBill}, a...)...)
	}
	switch {
	case b.Direction != Payable && b.Direction != Receivable:
		return fail("direction must be payable or receivable")
	case b.Amount <= 0 || b.Amount > MaxLegAmount:
		return fail("amount must be between 1 and %d centavos", MaxLegAmount)
	case b.AccountID == "":
		return fail("account is required")
	case b.CategoryID == "":
		return fail("category is required")
	case b.Competence.IsZero():
		return fail("competence date is required")
	case b.Due.IsZero():
		return fail("due date is required")
	case len(b.Description) > maxBillDescription:
		return fail("description is at most %d characters", maxBillDescription)
	case b.Status != BillForecast && b.Status != BillPaid && b.Status != BillCanceled:
		return fail("unknown status %q", b.Status)
	case b.Status == BillPaid && b.PaidDate.IsZero():
		return fail("a paid bill needs its paid date")
	}
	switch b.Origin {
	case OriginManual:
	case OriginRecurrence, OriginBillingInvoice, OriginCardStatement, OriginImport:
		if strings.TrimSpace(b.OriginRef) == "" {
			return fail("origin %s needs its reference", b.Origin)
		}
	default:
		return fail("unknown origin %q", b.Origin)
	}
	return nil
}

// Facts is the view the posting rules take.
func (b Bill) Facts() BillFacts {
	f := BillFacts{Direction: b.Direction, Amount: b.Amount, CategoryID: b.CategoryID, AccountID: b.AccountID}
	if b.Origin == OriginCardStatement {
		f.Clears = b.CategoryID // the card
	}
	return f
}

// CanEdit: a bill is editable only while it is a forecast.
func (b Bill) CanEdit() bool { return b.Status == BillForecast }

// CanSettle refuses anything but a forecast.
func (b Bill) CanSettle() error {
	if b.Status != BillForecast {
		return fmt.Errorf("%w: a %s bill cannot be settled", ErrBillState, b.Status)
	}
	return nil
}

// CanCancel refuses a paid bill: it is corrected by reversing its settlement
// first, so both stay visible.
func (b Bill) CanCancel() error {
	if b.Origin == OriginCardStatement {
		return fmt.Errorf("%w: uma fatura fechada não é cancelada; uma correção entra na próxima fatura", ErrBillState)
	}
	switch b.Status {
	case BillForecast:
		return nil
	case BillPaid:
		return fmt.Errorf("%w: a paid bill is corrected by reversing its settlement first", ErrBillState)
	default:
		return fmt.Errorf("%w: the bill is already canceled", ErrBillState)
	}
}

type Bucket string

const (
	BucketOverdue  Bucket = "overdue"
	BucketToday    Bucket = "today"
	BucketUpcoming Bucket = "upcoming"
)

// BucketOn places a bill relative to a civil day: F2's "vencidos, hoje, a vencer".
func (b Bill) BucketOn(today brcal.Date) Bucket {
	switch c := b.Due.Compare(today); {
	case c < 0:
		return BucketOverdue
	case c == 0:
		return BucketToday
	default:
		return BucketUpcoming
	}
}

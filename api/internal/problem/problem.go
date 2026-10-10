// Package problem is billing's RFC 7807 layer, built on the shared
// api-commons/problem constructors so every CTech service emits the same error
// shape. Only the Fiber-facing part and the billing-specific type URIs live here.
package problem

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	fiberobs "gopkg.aoctech.app/api-commons/observability/fiber"
	commonproblem "gopkg.aoctech.app/api-commons/problem"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

const ContentType = "application/problem+json"

// Billing-specific problem types. Constants, never string literals at call
// sites: a client that branches on `type` is broken by a typo nobody notices.
const (
	TypeIdempotencyConflict = "/problems/idempotency-conflict"
	TypePayoutNotEnabled    = "/problems/payout-not-enabled"
	TypeInvalidTransition   = "/problems/invalid-transition"
	TypeConcurrentUpdate    = "/problems/concurrent-update"
	TypeAlreadyGenerated    = "/problems/already-generated"
	// TypeNoBillingAccount is a signed-in person who has no customer record in
	// tenant zero. It stays a 403 — the refusal is real and the portal has
	// nothing to show them — but it is the one 403 the portal renders as an
	// empty state rather than a failure, so it needs a name a client can branch
	// on. `detail` is prose and prose gets rewritten.
	//
	// It discloses nothing the body did not already say, and nothing about
	// anybody else: that this reader has no billing account is a fact they
	// already hold.
	TypeNoBillingAccount = "/problems/no-billing-account"
	// TypeSpaceNotFound is every reason a finance space is refused — not a
	// member, no such organization, malformed id — in one identical response.
	TypeSpaceNotFound = "/problems/space-not-found"
	// TypeSpaceUnavailable is ctech-account being unreachable: access fails
	// closed, and the body says nothing about any organization.
	TypeSpaceUnavailable = "/problems/space-unavailable"
)

// FieldError is a single field-level validation failure.
//
// Code is the stable, machine-readable reason ("required", "too_long", ...) the
// client maps to its own translated text; Params carries the numbers the old
// prose used to embed (e.g. {"max": 64}). Message is an English fallback for
// logs and API integrators, never meant for end users.
type FieldError struct {
	Field   string         `json:"field"` // dotted JSON path, e.g. "items[0].quantity"
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}

// Field builds a FieldError. params is an alternating key, value list.
func Field(field, code, message string, params ...any) FieldError {
	fe := FieldError{Field: field, Code: code, Message: message}
	for i := 0; i+1 < len(params); i += 2 {
		if fe.Params == nil {
			fe.Params = map[string]any{}
		}
		fe.Params[params[i].(string)] = params[i+1]
	}
	return fe
}

// Problem is an RFC 7807 body that knows how to write itself to a Fiber
// response.
//
// Code is the stable machine-readable identifier of the specific failure. It is
// always present; when a call site does not name one it defaults to the type's
// slug ("/problems/not-found" -> "not_found"). Title and Detail are English
// and are not meant to be shown to end users: the client translates by Code.
// Errors (validation only) shadows the shared body's field list so each entry
// can carry a code and params.
type Problem struct {
	commonproblem.Problem
	Code   string       `json:"code"`
	Errors []FieldError `json:"errors,omitempty"`
}

func wrap(p *commonproblem.Problem) *Problem {
	return &Problem{Problem: *p, Code: slugCode(p.Type)}
}

// slugCode derives the default code from a type URI.
func slugCode(typ string) string {
	i := strings.LastIndexByte(typ, '/')
	return strings.ReplaceAll(typ[i+1:], "-", "_")
}

// WithCode names the specific failure, overriding the type-derived default.
func (p *Problem) WithCode(code string) *Problem {
	p.Code = code
	return p
}

func (p *Problem) WithCause(err error) *Problem {
	p.Problem.WithCause(err)
	return p
}

// Send writes the problem with the correct content type.
//
// The content type is passed to JSON rather than set beforehand: Fiber's JSON
// helper sets it itself, so a Set() before the call is silently overwritten and
// every error goes out as plain application/json. A client that branches on the
// problem content type would never see one.
func (p *Problem) Send(c fiber.Ctx) error {
	fiberobs.LogHTTPError(c, p.Status, p.Type, p.Cause())
	return c.Status(p.Status).JSON(p, ContentType)
}

func BadRequest(detail string) *Problem   { return wrap(commonproblem.BadRequest(detail)) }
func Unauthorized(detail string) *Problem { return wrap(commonproblem.Unauthorized(detail)) }
func Forbidden(detail string) *Problem    { return wrap(commonproblem.Forbidden(detail)) }
func NotFound(detail string) *Problem     { return wrap(commonproblem.NotFound(detail)) }
func Conflict(detail string) *Problem     { return wrap(commonproblem.Conflict(detail)) }
func Unprocessable(detail string) *Problem {
	return wrap(commonproblem.UnprocessableEntity(detail))
}
func Internal(detail string) *Problem { return wrap(commonproblem.InternalServer(detail)) }

// Validation returns a 422 carrying field-level failures.
func Validation(errs []FieldError) *Problem {
	p := wrap(commonproblem.Validation(nil))
	p.Errors = errs
	return p
}

// New builds a problem with an explicit type URI.
func New(status int, typ, title, detail string) *Problem {
	return wrap(commonproblem.New(status, typ, title, detail))
}

// FromError maps a domain or repository error to the right status.
//
// It exists so that the mapping is decided once. Doing it per handler is how a
// concurrent-update error becomes a 500 on one route and a 409 on another, and
// how a client learns that retrying is pointless from the wrong status code.
//
// An unrecognised error becomes a 500 with a generic detail: the internal
// message is logged, never returned. Error strings leak table names, key
// structure and internal ids to whoever is probing the API.
func FromError(err error) *Problem {
	switch {
	case err == nil:
		return nil

	case errors.Is(err, fiber.ErrNotFound):
		return NotFound("resource not found").WithCode("resource_not_found")

	case errors.Is(err, repositories.ErrNotFound):
		return NotFound("resource not found").WithCode("resource_not_found")

	case errors.Is(err, space.ErrDenied):
		return Forbidden("your role does not allow this operation").WithCode("role_denied")

	case errors.Is(err, finance.ErrBillState), errors.Is(err, repositories.ErrAlreadyReversed):
		return New(409, TypeInvalidTransition, "Invalid Transition", err.Error())

	case errors.Is(err, repositories.ErrCardMoved):
		return New(409, TypeConcurrentUpdate, "Concurrent Update", "The card changed while you were recording. Try again.").WithCode("card_moved")

	case errors.Is(err, repositories.ErrPurchaseRefunded):
		return New(409, TypeInvalidTransition, "Invalid Transition", "This purchase has already been refunded.").WithCode("purchase_refunded")

	case errors.Is(err, repositories.ErrNotClosable):
		return New(409, TypeInvalidTransition, "Invalid Transition", "This statement is not open for closing.").WithCode("statement_not_closable")

	case errors.Is(err, repositories.ErrNothingToAdvance):
		return New(409, TypeInvalidTransition, "Invalid Transition", "There are no future installments to advance.").WithCode("nothing_to_advance")

	case errors.Is(err, finance.ErrInvalidCard):
		return Unprocessable(err.Error()).WithCode("invalid_card")

	case errors.Is(err, finance.ErrInvalidInstallments):
		return Unprocessable(err.Error()).WithCode("invalid_installments")

	case errors.Is(err, repositories.ErrOpeningExists):
		return New(409, TypeInvalidTransition, "Invalid Transition",
			"This account already has an opening balance. Reverse the current one to post another.").WithCode("opening_balance_exists")

	case errors.Is(err, repositories.ErrAccountHasEntries):
		return New(409, TypeInvalidTransition, "Invalid Transition",
			"This account already has entries, so the statement's balance is not its opening balance.").WithCode("account_has_entries")

	case errors.Is(err, repositories.ErrNoStatementBalance):
		return Unprocessable("this statement declares no balance (LEDGERBAL)").WithCode("no_statement_balance")

	case errors.Is(err, repositories.ErrNotManual):
		return New(409, TypeInvalidTransition, "Invalid Transition",
			"Only transfers and opening balances are reversed from the statement. For a payment, use Undo payment.").WithCode("not_reversible_entry")

	case errors.Is(err, repositories.ErrLineResolved):
		return New(409, TypeInvalidTransition, "Invalid Transition", "This statement line was already reconciled.").WithCode("line_already_reconciled")
	case errors.Is(err, repositories.ErrRecurrenceWouldEnd):
		return Unprocessable("this end date leaves the recurrence with no occurrence to come; send archive:true to end and archive it").WithCode("recurrence_would_end")
	case errors.Is(err, repositories.ErrEndCancelsBills):
		return Unprocessable("this end date cancels the unpaid bills the recurrence made after it; send cancel_after_end:true to confirm").WithCode("end_cancels_bills")
	case errors.Is(err, repositories.ErrEndIncomplete):
		// Before the generic conflict case: a retry of the same request finishes it.
		return New(409, TypeConcurrentUpdate, "Concurrent Update",
			"the end was saved, but some bills after it could not be cancelled; send the same request again").WithCode("recurrence_end_incomplete")
	case errors.Is(err, repositories.ErrBillLinked):
		return New(409, TypeInvalidTransition, "Invalid Transition", "This bill is already linked to another statement line.").WithCode("bill_already_linked")
	case errors.Is(err, repositories.ErrLineMismatch):
		return Unprocessable("the bill is not of this line's account or direction").WithCode("line_bill_mismatch")
	case errors.Is(err, statement.ErrCardStatement):
		return Unprocessable(err.Error()).WithCode("statement_card_not_supported")
	case errors.Is(err, statement.ErrManyAccounts):
		return Unprocessable(err.Error()).WithCode("statement_many_accounts")
	case errors.Is(err, statement.ErrCurrency):
		return Unprocessable(err.Error()).WithCode("statement_currency")
	case errors.Is(err, statement.ErrTooLarge), errors.Is(err, statement.ErrTooMany):
		return Unprocessable(err.Error()).WithCode("statement_too_large")
	case errors.Is(err, statement.ErrEmpty):
		return Unprocessable(err.Error()).WithCode("statement_empty")
	case errors.Is(err, statement.ErrUnreadable):
		return Unprocessable(err.Error()).WithCode("statement_unreadable")
	case errors.Is(err, statement.ErrNoMapping):
		return Unprocessable(err.Error()).WithCode("csv_mapping_required")
	case errors.Is(err, statement.ErrInvalidMapping):
		return Unprocessable(err.Error()).WithCode("invalid_csv_mapping")

	case errors.Is(err, repositories.ErrUnknownAccount):
		return Unprocessable("unknown account or category in this space").WithCode("unknown_account")

	case errors.Is(err, finance.ErrInvalidBill):
		return Unprocessable(err.Error()).WithCode("invalid_bill")
	case errors.Is(err, finance.ErrInvalidRecurrence):
		return Unprocessable(err.Error()).WithCode("invalid_recurrence")
	case errors.Is(err, finance.ErrInvalidTransaction):
		return Unprocessable(err.Error()).WithCode("invalid_transaction")
	case errors.Is(err, finance.ErrInvalidAccount):
		return Unprocessable(err.Error()).WithCode("invalid_account")

	case errors.Is(err, billing.ErrPayoutNotEnabled):
		return New(409, TypePayoutNotEnabled, "Payout Not Enabled",
			"this organization cannot open charges yet")

	case errors.Is(err, billing.ErrInvalidTransition), errors.Is(err, billing.ErrCauseNotAllowed):
		return New(409, TypeInvalidTransition, "Invalid Transition", err.Error())

	case errors.Is(err, repositories.ErrConcurrentModification):
		return New(409, TypeConcurrentUpdate, "Concurrent Update",
			"the resource changed since it was read; reload and try again")

	// A write cancelled by another transaction on the same items (api-commons
	// v1.11.0 no longer reads it as a failed condition): nothing was written, the
	// same request may be repeated. Not a 500. Cross-repo candidate: this
	// mapping belongs in api-commons `problem`, for every service.
	case errors.Is(err, repositories.ErrTransactionConflict), repositories.IsTransactionConflict(err):
		return New(409, TypeConcurrentUpdate, "Concurrent Update",
			"another request was changing the same data; try again").WithCode("concurrent_update")

	case errors.Is(err, repositories.ErrAttemptExists):
		// Two "pay" clicks arrived together. The caller re-reads and shows the
		// charge that already exists — which is why this is a 409 and not a 500:
		// retrying is not merely allowed, it is the fix.
		return New(409, TypeConcurrentUpdate, "Concurrent Update",
			"another payment attempt for this invoice is in progress; reload and try again").WithCode("payment_attempt_in_progress")

	case errors.Is(err, repositories.ErrAlreadyGenerated):
		return New(409, TypeAlreadyGenerated, "Already Generated",
			"this period has already been invoiced")

	case errors.Is(err, repositories.ErrDuplicateUsage):
		// A repeated usage report is the caller's retry succeeding, not a
		// failure. Reporting it as an error would make every well-behaved
		// integrator log an error on every retry.
		return nil

	case errors.Is(err, billing.ErrMetadataInvalid):
		return Unprocessable(err.Error()).WithCode("invalid_metadata")
	case errors.Is(err, billing.ErrInvalidRecurrence):
		return Unprocessable(err.Error()).WithCode("invalid_recurrence")
	case errors.Is(err, billing.ErrInvalidPrice):
		return Unprocessable(err.Error()).WithCode("invalid_price")
	case errors.Is(err, billing.ErrInvalidUsage):
		return Unprocessable(err.Error()).WithCode("invalid_usage")
	case errors.Is(err, billing.ErrInvalidSubscriptionItem):
		return Unprocessable(err.Error()).WithCode("invalid_subscription_item")
	case errors.Is(err, billing.ErrInvalidCreditNote):
		return Unprocessable(err.Error()).WithCode("invalid_credit_note")
	case errors.Is(err, billing.ErrInvalidDunningPolicy):
		return Unprocessable(err.Error()).WithCode("invalid_dunning_policy")
	case errors.Is(err, billing.ErrInvoiceItems):
		return Unprocessable(err.Error()).WithCode("invalid_invoice_items")

	default:
		return Internal("internal error")
	}
}

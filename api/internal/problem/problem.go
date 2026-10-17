// Package problem is billing's RFC 7807 layer, built on the shared
// api-commons/problem constructors so every CTech service emits the same error
// shape. Only the Fiber-facing part and the billing-specific type URIs live here.
package problem

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/byok"
	fiberobs "gopkg.aoctech.app/api-commons/observability/fiber"
	commonproblem "gopkg.aoctech.app/api-commons/problem"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
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
	// TypeTooManyRequests: the same request again too soon (a webhook test).
	TypeTooManyRequests = commonproblem.TypeTooManyRequests
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
	// TypeAccountLocked is a signed-in person whose CTech account is being
	// deleted (ctech-account's erasure saga). Every session request is refused
	// from the lock to the purge, even with a token that is still valid.
	TypeAccountLocked = "/problems/account-locked"
	// TypeProviderUnavailable is a merchant's bank (BYOK) or billing's outbound
	// Lambda not answering. Retryable; nothing was decided.
	TypeProviderUnavailable = "/problems/provider-unavailable"
	// TypeAccountUnavailable: ctech-account did not answer a question billing
	// must ask before acting (the owner of an OAuth client). Retryable.
	TypeAccountUnavailable = "/problems/account-unavailable"
	// TypeNoMerchant is an organization the person may administer that has no
	// billing tenant in the requested mode. The console renders it as the
	// invitation to bill through CTech Billing (spec § 7.1), so a client
	// branches on it; it says nothing a member of the organization does not
	// already know, and only members get this far.
	TypeNoMerchant = "/problems/no-merchant"
	// TypeTenantSuspended is a live tenant whose prod_billing entitlement ended:
	// writes refused, reads served (spec § 4.3).
	TypeTenantSuspended = "/problems/tenant-suspended"
	// TypeCompaniesUnavailable is ctech-account not answering about an
	// organization's companies: the application form cannot be filled now.
	TypeCompaniesUnavailable = "/problems/companies-unavailable"
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
	// UpgradePlan and Quota go with a plan limit (BYOK part C): the console
	// links to the plan screen.
	UpgradePlan bool   `json:"upgrade_plan,omitempty"`
	Quota       *int64 `json:"quota,omitempty"`
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

	// BYOK (spec 2026-10-11 § 12.1). Details are fixed text: a provider's own
	// message never reaches a client.
	case errors.Is(err, billing.ErrTenantSuspended):
		return Forbidden("this organization's live billing is suspended").WithCode("tenant_suspended")
	case errors.Is(err, services.ErrSandboxWebhookShared):
		return Conflict("the shared sandbox webhook is managed by CTech").WithCode("byok_sandbox_webhook_shared")
	case errors.Is(err, services.ErrTenantZeroRail):
		return Conflict("tenant zero's collection rail is changed by the operator").WithCode("tenant_zero_rail")
	case errors.Is(err, services.ErrNoHooksHost):
		return New(503, TypeProviderUnavailable, "Provider Unavailable", "payment notices are not available in this deployment").WithCode("byok_webhook_unavailable")
	case errors.Is(err, billing.ErrInvalidCollectionSettings):
		return Unprocessable("late fee up to 20% and monthly interest up to 10%").WithCode("invalid_collection_settings")
	case errors.Is(err, services.ErrRefundWindowClosed):
		return Unprocessable("the 90-day window to refund this Pix has closed").WithCode("refund_window_closed")
	case errors.Is(err, services.ErrCustomerTaxIDRequired):
		return Unprocessable("the customer has no CPF or CNPJ; add one to create the Pix").WithCode("customer_tax_id_required")
	case errors.Is(err, services.ErrByokChargeOpen):
		return Conflict("the invoice's Pix is still payable").WithCode("byok_charge_open")
	case errors.Is(err, services.ErrNotByokRail):
		return Conflict("this organization does not collect through a bank connection").WithCode("byok_not_collecting")
	case errors.Is(err, billing.ErrByokNotConnected):
		return Conflict("no active bank connection for collection").WithCode("byok_not_connected")
	case errors.Is(err, billing.ErrByokCertificateExpired):
		return Conflict("the bank connection's certificate expired").WithCode("byok_certificate_expired")
	case errors.Is(err, billing.ErrByokScopesMissing):
		return Conflict("the bank integration lacks the collection scopes").WithCode("byok_collection_scopes_missing")
	case errors.Is(err, billing.ErrSubscriptionLimitReached), errors.Is(err, billing.ErrBankConnectionLimitReached):
		code, detail := "subscription_limit_reached", "the plan's active subscriptions are at its quota; upgrade to add more"
		if errors.Is(err, billing.ErrBankConnectionLimitReached) {
			code, detail = "bank_connection_limit_reached", "the plan's bank connections are at its quota; upgrade to add more"
		}
		p := Conflict(detail).WithCode(code)
		p.UpgradePlan = true
		if le := (*billing.LimitError)(nil); errors.As(err, &le) {
			p.Quota = &le.Quota
		}
		return p
	case errors.Is(err, services.ErrUnknownPlan):
		return Validation([]FieldError{Field("plan", "invalid", "choose one of the public plans")})
	case errors.Is(err, services.ErrNoBillingPlan):
		return Conflict("the organization has no plan to change").WithCode("plan_not_found")
	case errors.Is(err, billing.ErrByokQuotaExceeded):
		p := Conflict("the plan's charges are used up this month; upgrade to keep collecting").WithCode("byok_quota_exceeded")
		p.UpgradePlan = true
		return p
	case errors.Is(err, services.ErrByokSpaceKind):
		return New(404, TypeSpaceNotFound, "Space not found", "space not found")
	case errors.Is(err, services.ErrByokNotConnected):
		return NotFound("this organization has not connected this provider").WithCode("byok_not_connected")
	case errors.Is(err, services.ErrConnectNotAuthorized):
		return Validation([]FieldError{Field("authorized", "required", "the authorization must be accepted")})
	case errors.Is(err, services.ErrBackfillRange):
		return Validation([]FieldError{Field("backfill_months", "out_of_range", "backfill_months is 3 to 12", "min", 3, "max", 12)})
	case errors.Is(err, services.ErrSandboxInLive):
		return Conflict("the shared sandbox is test mode only").WithCode("byok_sandbox_test_only")
	case errors.Is(err, repositories.ErrConnectionChanged):
		return New(409, TypeConcurrentUpdate, "Concurrent Update", "the connection changed meanwhile; try again").WithCode("byok_connection_changed")
	case errors.Is(err, repositories.ErrConnectionExists):
		return Conflict("this organization already has a connection to this provider").WithCode("byok_already_connected")
	case errors.Is(err, byok.ErrCertificateExpired):
		return Unprocessable("the certificate has expired; renew it at the bank").WithCode("byok_certificate_expired")
	case errors.Is(err, byok.ErrCertificateInvalid):
		return Unprocessable("the certificate or the key is not a valid PEM file").WithCode("byok_certificate_invalid")
	case errors.Is(err, byok.ErrKeyMismatch):
		return Unprocessable("the key does not belong to this certificate").WithCode("byok_key_mismatch")
	case errors.Is(err, byok.ErrCredentialsRefused), errors.Is(err, byok.ErrScopesRefused):
		return Unprocessable("the bank refused these credentials").WithCode("byok_credentials_refused")
	case errors.Is(err, services.ErrPixKeyUnknown):
		return Unprocessable("the bank has no such Pix key on this account").WithCode("byok_pix_key_unknown")
	case errors.Is(err, byok.ErrUnauthorized):
		// The bank issued a token and then refused it on the first call (connect
		// checks the account with a balance read). That is the account number or
		// a missing permission, which the person can fix, not a server fault.
		return Unprocessable("the bank accepted the certificate but refused the account or the permissions").WithCode("byok_bearer_refused")
	case errors.Is(err, byokrpc.ErrSandboxBusy):
		return New(429, TypeTooManyRequests, "Too Many Requests", "the shared sandbox is busy; try again in a moment").WithCode("byok_sandbox_busy")
	case errors.Is(err, byok.ErrUnavailable), errors.Is(err, byok.ErrRateLimited):
		return New(503, TypeProviderUnavailable, "Provider Unavailable", "the bank could not be reached; try again later").WithCode("byok_provider_unavailable")
	case errors.Is(err, services.ErrSyncTooSoon):
		return New(429, TypeTooManyRequests, "Too Many Requests", "synced less than 10 minutes ago").WithCode("byok_sync_too_soon")
	case errors.Is(err, byok.ErrInvalidRequest):
		return Unprocessable("the bank connection request is invalid").WithCode("byok_invalid_request")

	case errors.Is(err, billing.ErrApplicationNotPending):
		return Conflict("the application was already decided").WithCode("application_not_pending")
	case errors.Is(err, billing.ErrAlreadyMerchant):
		return Conflict("this organization already bills in live mode").WithCode("already_merchant")
	case errors.Is(err, repositories.ErrApplicationBusy):
		return Conflict("another decision on this application is in progress").WithCode("application_busy")

	case errors.Is(err, space.ErrDenied):
		return Forbidden("your role does not allow this operation").WithCode("role_denied")

	case errors.Is(err, finance.ErrBillState), errors.Is(err, repositories.ErrAlreadyReversed):
		return New(409, TypeInvalidTransition, "Invalid Transition", err.Error())

	case errors.Is(err, repositories.ErrCardMoved):
		return New(409, TypeConcurrentUpdate, "Concurrent Update", "The card changed while you were recording. Try again.").WithCode("card_moved")
	case errors.Is(err, repositories.ErrRecurrenceChanged):
		return New(409, TypeConcurrentUpdate, "Concurrent Update", "The recurrence changed while this occurrence was being confirmed. Refresh and try again.").WithCode("recurrence_changed")
	case errors.Is(err, repositories.ErrOccurrenceAlreadyHandled):
		return New(409, TypeInvalidTransition, "Occurrence Already Handled", "This recurring occurrence is no longer waiting for confirmation.").WithCode("occurrence_not_pending")

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

	case errors.Is(err, repositories.ErrNotUnclassified):
		return Conflict("this bank line was already classified").WithCode("line_already_classified")
	case errors.Is(err, repositories.ErrAccountIsSynced):
		return Conflict("this account is synced from the bank; its movements come from the statement").WithCode("account_is_synced")
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

	case errors.Is(err, services.ErrPayoutConflict):
		return Conflict("this reference already names another payout").WithCode("payout_reference_conflict")
	case errors.Is(err, services.ErrPayoutNotAllowed):
		return Conflict("this tenant has no bank connection that can pay out").WithCode("payout_not_allowed")
	case errors.Is(err, services.ErrPayoutInvalid):
		return Unprocessable("the payout request is invalid").WithCode("invalid_payout")
	case errors.Is(err, services.ErrOneOffConflict):
		return Conflict("this reference already names another invoice").WithCode("reference_conflict")
	case errors.Is(err, services.ErrOneOffInvalid):
		return Unprocessable("the invoice request is invalid").WithCode("invalid_one_off_invoice")

	case errors.Is(err, repositories.ErrAlreadyGenerated):
		return New(409, TypeAlreadyGenerated, "Already Generated",
			"this period has already been invoiced")

	case errors.Is(err, repositories.ErrLevelKeyReused):
		return New(409, TypeIdempotencyConflict, "Idempotency Conflict",
			"this idempotency_key was already used for another level").WithCode("idempotency_key_reused")
	case errors.Is(err, repositories.ErrUserAlreadyCustomer):
		return New(409, TypeInvalidTransition, "Already a Customer",
			"this account is already a customer of this organization").WithCode("user_already_customer")
	case errors.Is(err, repositories.ErrPlanBusy):
		return New(409, TypeConcurrentUpdate, "Concurrent Update", "another plan choice is in progress; try again").WithCode("concurrent_update")
	case errors.Is(err, services.ErrNotAPlanPrice):
		return Unprocessable(err.Error()).WithCode("not_a_plan_price")
	case errors.Is(err, services.ErrCustomerRefMismatch):
		return New(409, TypeInvalidTransition, "Customer Mismatch",
			"this account's billing customer is not set up for plans; contact support").WithCode("customer_ref_mismatch")
	case errors.Is(err, services.ErrNoFinancePlan):
		return NotFound("no plan to cancel").WithCode("no_finance_plan")
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

// Package problem is billing's RFC 7807 layer, built on the shared
// api-commons/problem constructors so every CTech service emits the same error
// shape. Only the Fiber-facing part and the billing-specific type URIs live here.
package problem

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	commonproblem "gopkg.aoctech.app/api-commons/problem"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
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
)

// FieldError is a single field-level validation failure.
type FieldError = commonproblem.FieldError

// Problem is an RFC 7807 body that knows how to write itself to a Fiber
// response.
type Problem struct {
	commonproblem.Problem
}

func wrap(p *commonproblem.Problem) *Problem { return &Problem{Problem: *p} }

// Send writes the problem with the correct content type.
//
// The content type is passed to JSON rather than set beforehand: Fiber's JSON
// helper sets it itself, so a Set() before the call is silently overwritten and
// every error goes out as plain application/json. A client that branches on the
// problem content type would never see one.
func (p *Problem) Send(c fiber.Ctx) error {
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
func Validation(errs []FieldError) *Problem { return wrap(commonproblem.Validation(errs)) }

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
		return NotFound("recurso não encontrado")

	case errors.Is(err, repositories.ErrNotFound):
		return NotFound("recurso não encontrado")

	case errors.Is(err, billing.ErrPayoutNotEnabled):
		p := New(409, TypePayoutNotEnabled, "Payout Not Enabled",
			"esta organização ainda não pode abrir cobranças")
		return p

	case errors.Is(err, billing.ErrInvalidTransition), errors.Is(err, billing.ErrCauseNotAllowed):
		return New(409, TypeInvalidTransition, "Invalid Transition", err.Error())

	case errors.Is(err, repositories.ErrConcurrentModification):
		return New(409, TypeConcurrentUpdate, "Concurrent Update",
			"o recurso mudou desde a leitura; releia e tente de novo")

	case errors.Is(err, repositories.ErrAttemptExists):
		// Two "pagar" clicks arrived together. The caller re-reads and shows the
		// charge that already exists — which is why this is a 409 and not a 500:
		// retrying is not merely allowed, it is the fix.
		return New(409, TypeConcurrentUpdate, "Concurrent Update",
			"outra tentativa de pagamento para esta fatura está em andamento; releia e tente de novo")

	case errors.Is(err, repositories.ErrAlreadyGenerated):
		return New(409, TypeAlreadyGenerated, "Already Generated",
			"este período já foi faturado")

	case errors.Is(err, repositories.ErrDuplicateUsage):
		// A repeated usage report is the caller's retry succeeding, not a
		// failure. Reporting it as an error would make every well-behaved
		// integrator log an error on every retry.
		return nil

	case errors.Is(err, billing.ErrMetadataInvalid),
		errors.Is(err, billing.ErrInvalidRecurrence),
		errors.Is(err, billing.ErrInvalidPrice),
		errors.Is(err, billing.ErrInvalidUsage),
		errors.Is(err, billing.ErrInvalidSubscriptionItem),
		errors.Is(err, billing.ErrInvalidCreditNote),
		errors.Is(err, billing.ErrInvoiceItems):
		return Unprocessable(err.Error())

	default:
		return Internal("erro interno")
	}
}

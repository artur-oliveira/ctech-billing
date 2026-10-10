package v1

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type reportLevelRequest struct {
	CustomerRef    string `json:"customer_ref"`
	Meter          string `json:"meter"`
	Value          *int64 `json:"value"`
	OccurredAt     string `json:"occurred_at"`
	IdempotencyKey string `json:"idempotency_key"`
}

func ownerNotAllowed(c fiber.Ctx) error {
	return problem.Forbidden("this credential may not act for that owner").WithCode("owner_not_allowed").Send(c)
}

// reportLevel stores a level report (spec § 6.1): the whole current count of a
// meter for a customer reference. It needs no customer and creates none. Not
// behind the Idempotency-Key middleware: the body's key is the contract, and
// the repository enforces it with a marker (scope decision 8).
func (h *handlers) reportLevel(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req reportLevelRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.text("customer_ref", req.CustomerRef, true, limits.ExternalRef)
	ch.text("idempotency_key", req.IdempotencyKey, true, 255)
	if !billing.ValidMeter(req.Meter) {
		ch.fail("meter", "invalid_format", "lower-case letters, digits and _")
	}
	switch {
	case req.Value == nil:
		ch.fail("value", "required", "required")
	case *req.Value < 0 || *req.Value > limits.MaxUsageQuantity:
		ch.fail("value", "out_of_range", fmt.Sprintf("between 0 and %d", limits.MaxUsageQuantity), "min", 0, "max", limits.MaxUsageQuantity)
	}
	at, err := time.Parse(time.RFC3339, req.OccurredAt)
	switch {
	case req.OccurredAt == "":
		ch.fail("occurred_at", "required", "required")
	case err != nil:
		ch.fail("occurred_at", "invalid_format", "use RFC 3339", "format", "RFC3339")
	case at.Before(limits.MinDate.Time()) || at.After(h.now().Add(24*time.Hour)):
		ch.fail("occurred_at", "out_of_range", "outside the accepted range")
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}

	owners, err := h.meterOwners(c.Context(), t, req.Meter)
	if err != nil {
		return fail(c, err)
	}
	if len(owners) == 0 {
		return problem.Validation([]problem.FieldError{fieldErr("meter", "not_found", "no price is metered by this meter")}).Send(c)
	}
	if cred := middleware.GetCredential(c); cred != nil && cred.OwnerKey != "" && !slices.Contains(owners, cred.OwnerKey) {
		return problem.Forbidden("this credential may not report this meter").WithCode("meter_not_allowed").Send(c)
	}

	rec := &billing.LevelRecord{
		OrganizationID: t.OrganizationID, Livemode: t.Livemode, CustomerRef: req.CustomerRef, Meter: req.Meter,
		Value: *req.Value, OccurredAt: at, IdempotencyKey: req.IdempotencyKey,
	}
	switch err := h.levels.Append(c.Context(), rec, h.now()); {
	case err == nil:
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"recorded": true, "duplicate": false})
	case errors.Is(err, repositories.ErrDuplicateLevel):
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"recorded": true, "duplicate": true})
	default:
		return fail(c, err)
	}
}

// meterOwners lists the owner keys of the products whose prices are metered by
// meter (archived prices included: a subscription may still bill one).
func (h *handlers) meterOwners(ctx context.Context, t middleware.Tenant, meter string) ([]string, error) {
	prices, err := h.cat.ListPrices(ctx, t.OrganizationID, t.Livemode, pageLimit)
	if err != nil {
		return nil, err
	}
	var owners []string
	for _, p := range prices {
		if p.Meter != meter {
			continue
		}
		product, err := h.cat.GetProduct(ctx, t.OrganizationID, t.Livemode, p.ProductID)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(owners, product.OwnerKey) {
			owners = append(owners, product.OwnerKey)
		}
	}
	return owners, nil
}

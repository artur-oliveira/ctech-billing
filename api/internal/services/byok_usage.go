package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// BillingChargesMeter is the meter of the BYOK plans' overage price (spec § 12.5).
const BillingChargesMeter = "billing_charges"

// usageLookback is how many São Paulo days back the usage step reads: a
// charge is reportable 24 hours after its creation, and the extra days catch
// up after missed runs without an operator.
const usageLookback = 7

// ByokUsage is cmd/byok step 6 (spec § 5): each live BYOK charge that
// survives 24 hours is one unit of billing_charges on its organization's
// prod_billing subscription in tenant zero, reported once (idempotency key:
// the attempt id).
type ByokUsage struct {
	repo       *repositories.ByokRepository
	payments   *repositories.PaymentRepository
	orgs       *repositories.OrganizationRepository
	plans      *BillingPlans
	subs       *repositories.SubscriptionRepository
	catalog    *repositories.CatalogRepository
	usage      *repositories.UsageRepository
	tenantZero string
}

func NewByokUsage(repo *repositories.ByokRepository, payments *repositories.PaymentRepository, orgs *repositories.OrganizationRepository,
	plans *BillingPlans, subs *repositories.SubscriptionRepository, catalog *repositories.CatalogRepository,
	usage *repositories.UsageRepository, tenantZero string) *ByokUsage {
	return &ByokUsage{repo: repo, payments: payments, orgs: orgs, plans: plans, subs: subs, catalog: catalog, usage: usage, tenantZero: tenantZero}
}

// meteredTarget is where a tenant's charges are reported; item "" means its
// plan bills no overage (Grátis, early access, internal).
type meteredTarget struct {
	sub  *billing.Subscription
	item string
}

// Report walks the last days' unreported charges. A row leaves its day once
// reported or skipped, so re-running reports nothing twice.
func (u *ByokUsage) Report(ctx context.Context, now time.Time) (reported, skipped int, errs []error) {
	targets := map[string]meteredTarget{}
	today := brcal.FromTime(now)
	for back := usageLookback; back >= 0; back-- {
		var start map[string]types.AttributeValue
		for {
			page, err := u.repo.PendingUsage(ctx, today.AddDays(-back), start)
			if err != nil {
				return reported, skipped, append(errs, err)
			}
			for _, row := range page.Items {
				done, billed, err := u.reportOne(ctx, row, targets, now)
				switch {
				case err != nil:
					errs = append(errs, fmt.Errorf("byok usage %s: %w", row.AttemptID, err))
				case !done:
				case billed:
					reported++
				default:
					skipped++
				}
			}
			if page.LastEvaluatedKey == nil {
				break
			}
			start = page.LastEvaluatedKey
		}
	}
	return reported, skipped, errs
}

// reportOne answers done=false while the charge is younger than 24 hours.
func (u *ByokUsage) reportOne(ctx context.Context, row repositories.ByokUsageRow, targets map[string]meteredTarget, now time.Time) (done, billed bool, err error) {
	if now.Sub(row.CreatedAt) < 24*time.Hour {
		return false, false, nil
	}
	a, err := u.payments.GetAttempt(ctx, row.TenantID, true, row.InvoiceID, row.AttemptNumber)
	if err != nil {
		return false, false, err
	}
	if !a.CancelledAt.IsZero() && a.CancelledAt.Sub(a.CreatedAt) < 24*time.Hour {
		return true, false, u.repo.UsageDone(ctx, row)
	}
	t, ok := targets[row.TenantID]
	if !ok {
		if t, err = u.target(ctx, row.TenantID); err != nil {
			return false, false, err
		}
		targets[row.TenantID] = t
	}
	if t.item == "" {
		return true, false, u.repo.UsageDone(ctx, row)
	}
	rec := &billing.UsageRecord{ID: id.NewWithPrefix(id.PrefixUsageRecord), OrganizationID: u.tenantZero, Livemode: true,
		SubscriptionItemID: t.item, Quantity: 1, OccurredAt: a.CreatedAt, IdempotencyKey: a.ID}
	err = u.usage.Append(ctx, rec, t.sub.PeriodContaining(rec.Date()).Start, now)
	if err != nil && !errors.Is(err, repositories.ErrDuplicateUsage) {
		return false, false, err
	}
	return true, true, u.repo.UsageDone(ctx, row)
}

// target is the tenant's billing_charges item, read once per run.
func (u *ByokUsage) target(ctx context.Context, tenantID string) (meteredTarget, error) {
	org, err := u.orgs.Get(ctx, tenantID, true)
	if errors.Is(err, repositories.ErrNotFound) {
		// The tenant is gone (account deletion): nothing left to bill.
		return meteredTarget{}, nil
	}
	if err != nil {
		return meteredTarget{}, err
	}
	sub, err := u.plans.Subscription(ctx, org)
	if err != nil || sub == nil {
		return meteredTarget{}, err
	}
	items, err := u.subs.ListItems(ctx, u.tenantZero, true, sub.ID)
	if err != nil {
		return meteredTarget{}, err
	}
	for _, it := range items {
		price, err := u.catalog.GetPrice(ctx, u.tenantZero, true, it.PriceID)
		if err != nil {
			return meteredTarget{}, err
		}
		if price.Meter == BillingChargesMeter {
			return meteredTarget{sub: sub, item: it.ID}, nil
		}
	}
	return meteredTarget{sub: sub}, nil
}

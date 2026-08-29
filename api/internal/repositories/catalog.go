package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// CatalogRepository stores products and prices.
//
// **There is no UpdatePrice, and that absence is the feature.** A price is
// immutable (ADR 0001's model, assessment § 5.2): changing what something costs
// means creating a new Price, and existing subscriptions keep pointing at the
// old one. Grandfathering is then a consequence of the model rather than a flag
// someone has to remember to set. Archive only hides a price from the catalogue.
//
// Products and prices are two tables. A price is not a child of a product the
// way an invoice line is a child of an invoice — nothing reads a product
// together with its prices in one query, and the two are listed separately by
// the console. Nesting them would be an aggregate that exists only in the key.
type CatalogRepository struct {
	products Base
	prices   Base
	audit    Base
}

func NewCatalogRepository(db *dynamodb.Client, cfg *config.Config) *CatalogRepository {
	return &CatalogRepository{
		products: NewBase(db, cfg, TableProducts),
		prices:   NewBase(db, cfg, TablePrices),
		audit:    NewBase(db, cfg, TableAudit),
	}
}

// CreateProduct writes a new product, with the audit row that says who added it.
//
// The audit is in the transaction rather than beside it, for the same reason it
// is on every status change: "who put this in the catalogue" is asked about a
// price nobody recognises, and it is answerable only if the answer was recorded
// at the time. The actor is required — a catalogue entry that appeared by itself
// is the one nobody can explain.
func (r *CatalogRepository) CreateProduct(ctx context.Context, p *billing.Product, actor, requestID string, now time.Time) error {
	if err := p.Metadata.Validate(); err != nil {
		return err
	}
	if actor == "" {
		return fmt.Errorf("repositories: creating product %s needs an actor", p.ID)
	}
	item, err := Encode(productRow{
		keys:    newKeys(TenantPK(p.OrganizationID, p.Livemode), ProductSK(p.ID), RetentionProduct, now),
		Product: *p,
	})
	if err != nil {
		return err
	}
	auditItem, err := buildAuditItem(p.OrganizationID, p.Livemode, AuditEntry{
		Entity:    EntityProduct,
		EntityID:  p.ID,
		Action:    "product.created",
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		After:     p.Name,
	}, "", "", now)
	if err != nil {
		return err
	}
	err = r.products.TransactWrite(ctx, txItems(
		r.products.BuildPutTxItemIfAbsent(item),
		r.audit.BuildPutTxItemIfAbsent(auditItem),
	))
	if IsConditionFailed(err) {
		return fmt.Errorf("product %s already exists", p.ID)
	}
	return err
}

// SetProductDunningPolicy overrides (or clears) a product's dunning schedule.
//
// The one mutation a product accepts besides existing, and deliberately not
// modelled as "editing the product": what changes is an operational decision
// about chasing unpaid invoices, not what the thing is or what it costs. Like
// the organization's, it touches no invoice — the schedule is copied onto an
// invoice at finalization.
func (r *CatalogRepository) SetProductDunningPolicy(
	ctx context.Context,
	p *billing.Product,
	policy billing.DunningSchedule,
	actor, requestID string,
	now time.Time,
) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if actor == "" {
		return fmt.Errorf("repositories: changing the dunning policy of product %s needs an actor", p.ID)
	}

	names := map[string]string{"#dp": "dunning_policy", "#ua": "updated_at"}
	values := map[string]types.AttributeValue{
		":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	expr := "SET #ua = :now REMOVE #dp"
	if len(policy) > 0 {
		encoded, err := attributevalue.Marshal(policy)
		if err != nil {
			return err
		}
		values[":dp"] = encoded
		expr = "SET #dp = :dp, #ua = :now"
	}

	auditItem, err := buildAuditItem(p.OrganizationID, p.Livemode, AuditEntry{
		Entity:    EntityProduct,
		EntityID:  p.ID,
		Action:    "product.dunning_policy_changed",
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		Before:    describeSchedule(p.DunningPolicy),
		After:     describeSchedule(policy),
	}, "", "", now)
	if err != nil {
		return err
	}

	update := r.products.BuildRawUpdateTxItem(
		TenantPK(p.OrganizationID, p.Livemode), new(ProductSK(p.ID)),
		expr, "attribute_exists(pk)", names, values,
	)
	if err := r.products.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem))); err != nil {
		return err
	}
	p.DunningPolicy = policy.Clone()
	return nil
}

// GetProduct reads a product.
func (r *CatalogRepository) GetProduct(ctx context.Context, organizationID string, livemode bool, productID string) (*billing.Product, error) {
	item, err := r.products.GetItem(ctx, TenantPK(organizationID, livemode), ProductSK(productID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: product %s", ErrNotFound, productID)
	}
	row, err := Decode[productRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Product, nil
}

// ListProducts returns the tenant's products.
func (r *CatalogRepository) ListProducts(ctx context.Context, organizationID string, livemode bool, limit int) ([]billing.Product, error) {
	res, err := r.products.Query(ctx, QueryOpts{
		PK:       TenantPK(organizationID, livemode),
		SKPrefix: skProduct,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[productRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Product, len(rows))
	for i, row := range rows {
		out[i] = row.Product
	}
	return out, nil
}

// CreatePrice writes a new price. It validates first, because an unbillable
// price stored is an invoice that fails at generation time — long after whoever
// created it has moved on.
//
// The write is create-only. A price id that already exists fails rather than
// overwriting, which is the storage-level half of immutability.
func (r *CatalogRepository) CreatePrice(ctx context.Context, p *billing.Price, actor, requestID string, now time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if actor == "" {
		return fmt.Errorf("repositories: creating price %s needs an actor", p.ID)
	}
	item, err := Encode(priceRow{
		keys:  newKeys(TenantPK(p.OrganizationID, p.Livemode), PriceSK(p.ID), RetentionPrice, now),
		Price: *p,
	})
	if err != nil {
		return err
	}
	// The amount is in the audit row on purpose. A price cannot be edited, so
	// this line is the whole history of what was decided — and "who set this to
	// R$ 199,00" is the question a disputed invoice turns into.
	auditItem, err := buildAuditItem(p.OrganizationID, p.Livemode, AuditEntry{
		Entity:    EntityPrice,
		EntityID:  p.ID,
		Action:    "price.created",
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		After:     p.UnitAmount.String(),
	}, "", "", now)
	if err != nil {
		return err
	}
	err = r.prices.TransactWrite(ctx, txItems(
		r.prices.BuildPutTxItemIfAbsent(item),
		r.audit.BuildPutTxItemIfAbsent(auditItem),
	))
	if IsConditionFailed(err) {
		return fmt.Errorf("price %s already exists (prices are immutable — create a new one)", p.ID)
	}
	return err
}

// GetPrice reads a price.
func (r *CatalogRepository) GetPrice(ctx context.Context, organizationID string, livemode bool, priceID string) (*billing.Price, error) {
	item, err := r.prices.GetItem(ctx, TenantPK(organizationID, livemode), PriceSK(priceID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: price %s", ErrNotFound, priceID)
	}
	row, err := Decode[priceRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Price, nil
}

// ArchivePrice hides a price from the catalogue.
//
// It is the **only** mutation a price accepts. Subscriptions already on it are
// untouched: archiving means "do not sell this any more", never "change what
// existing customers pay".
func (r *CatalogRepository) ArchivePrice(ctx context.Context, p *billing.Price, actor, requestID string, now time.Time) error {
	if actor == "" {
		return fmt.Errorf("repositories: archiving price %s needs an actor", p.ID)
	}
	auditItem, err := buildAuditItem(p.OrganizationID, p.Livemode, AuditEntry{
		Entity:    EntityPrice,
		EntityID:  p.ID,
		Action:    "price.archived",
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
	}, "false", "true", now)
	if err != nil {
		return err
	}
	// Conditional on the price not already being archived, which is what makes
	// a second click write no second audit row — the same discipline every
	// status change here follows.
	update := r.prices.BuildRawUpdateTxItem(
		TenantPK(p.OrganizationID, p.Livemode), new(PriceSK(p.ID)),
		"SET #ar = :true, #ua = :now",
		"attribute_exists(pk) AND (attribute_not_exists(#ar) OR #ar = :false)",
		map[string]string{"#ar": "archived", "#ua": "updated_at"},
		map[string]types.AttributeValue{
			":true":  &types.AttributeValueMemberBOOL{Value: true},
			":false": &types.AttributeValueMemberBOOL{Value: false},
			":now":   &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		},
	)
	err = r.prices.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: price %s is already archived", ErrConcurrentModification, p.ID)
	}
	if err != nil {
		return err
	}
	p.Archived = true
	return nil
}

// ListPrices returns the tenant's prices, archived ones included — the console
// needs to show them, because a subscription may still be on one.
func (r *CatalogRepository) ListPrices(ctx context.Context, organizationID string, livemode bool, limit int) ([]billing.Price, error) {
	res, err := r.prices.Query(ctx, QueryOpts{
		PK:       TenantPK(organizationID, livemode),
		SKPrefix: skPrice,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[priceRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.Price, len(rows))
	for i, row := range rows {
		out[i] = row.Price
	}
	return out, nil
}

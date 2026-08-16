package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

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
}

func NewCatalogRepository(db *dynamodb.Client, cfg *config.Config) *CatalogRepository {
	return &CatalogRepository{
		products: NewBase(db, cfg, TableProducts),
		prices:   NewBase(db, cfg, TablePrices),
	}
}

// CreateProduct writes a new product.
func (r *CatalogRepository) CreateProduct(ctx context.Context, p *billing.Product, now time.Time) error {
	if err := p.Metadata.Validate(); err != nil {
		return err
	}
	item, err := Encode(productRow{
		keys:    newKeys(TenantPK(p.OrganizationID, p.Livemode), ProductSK(p.ID), RetentionProduct, now),
		Product: *p,
	})
	if err != nil {
		return err
	}
	err = r.products.TransactWrite(ctx, txItems(r.products.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return fmt.Errorf("product %s already exists", p.ID)
	}
	return err
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
func (r *CatalogRepository) CreatePrice(ctx context.Context, p *billing.Price, now time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	item, err := Encode(priceRow{
		keys:  newKeys(TenantPK(p.OrganizationID, p.Livemode), PriceSK(p.ID), RetentionPrice, now),
		Price: *p,
	})
	if err != nil {
		return err
	}
	err = r.prices.TransactWrite(ctx, txItems(r.prices.BuildPutTxItemIfAbsent(item)))
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
func (r *CatalogRepository) ArchivePrice(ctx context.Context, p *billing.Price, now time.Time) error {
	updates := map[string]any{
		"archived":   true,
		"updated_at": now.UTC().Format(time.RFC3339Nano),
	}
	if _, err := r.prices.UpdateItem(ctx, TenantPK(p.OrganizationID, p.Livemode), strPtr(PriceSK(p.ID)), updates); err != nil {
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

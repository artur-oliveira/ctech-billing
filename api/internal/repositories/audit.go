package repositories

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// AuditRepository reads the append-only trail.
//
// It deliberately exposes **no update and no delete**. That absence is the
// guarantee: an audit log that can be edited answers nothing during an incident.
// Writes happen inside the transaction of the change they record — see
// CommitStatusChange — so there is no Append here either; a standalone
// "write an audit row" method is exactly the API that lets one go missing.
type AuditRepository struct {
	base Base
}

func NewAuditRepository(db *dynamodb.Client, cfg *config.Config) *AuditRepository {
	return &AuditRepository{base: NewBase(db, cfg, TableAudit)}
}

// ListForMonth returns the tenant's audit entries for a calendar month.
func (r *AuditRepository) ListForMonth(
	ctx context.Context,
	organizationID string,
	livemode bool,
	year, month, limit int,
	startKey map[string]types.AttributeValue,
) (*Page[billing.AuditLog], error) {
	return pagePeriod(ctx, r.base, organizationID, livemode, EntityAudit,
		PeriodPrefix(year, month, 0), limit, startKey,
		func(row auditRow) billing.AuditLog { return row.AuditLog })
}

// ListForEntity returns every audit entry about one entity, newest last.
//
// It reads the tenant's audit rows and filters by entity rather than adding an
// index. That is a deliberate, bounded compromise: this is the "who touched this
// invoice" panel, opened one entity at a time by a human, not a hot path. If it
// ever becomes one, the fix is an index — never a Scan.
func (r *AuditRepository) ListForEntity(
	ctx context.Context,
	organizationID string,
	livemode bool,
	entityID string,
	limit int,
) ([]billing.AuditLog, error) {
	// entity_id is a filter, and DynamoDB applies Limit to the rows it reads
	// before filtering: one call returned an empty trail whenever the entity's
	// entries came after the first Limit rows of the tenant's other activity.
	// Base.Query fills the page within a read budget per call; this keeps
	// following the cursor until limit entries are found or the range ends.
	if limit <= 0 {
		limit = 100
	}
	var items []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.Query(ctx, QueryOpts{
			PK:                TenantPK(organizationID, livemode),
			SKPrefix:          skAudit,
			FilterField:       "entity_id",
			FilterValue:       entityID,
			Limit:             limit - len(items),
			ScanIndexForward:  true,
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, err
		}
		items = append(items, res.Items...)
		if res.LastEvaluatedKey == nil || len(items) >= limit {
			break
		}
		start = res.LastEvaluatedKey
	}
	rows, err := DecodeItems[auditRow](items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = row.AuditLog
	}
	return out, nil
}

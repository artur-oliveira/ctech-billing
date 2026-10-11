package repositories

import (
	"context"
	"slices"

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

// Entity trail read budget: each Base.Query call evaluates up to
// entityAuditReadSize rows per DynamoDB call (10 calls, the api-commons
// default), and ListForEntity makes at most entityAuditMaxQueries of them — at
// most 10,000 audit rows per request, newest first.
const (
	entityAuditReadSize   = 200
	entityAuditMaxQueries = 5
	entityAuditMaxLimit   = 100
)

// ListForEntity returns the newest audit entries (up to limit, at most 100)
// about one entity, oldest first.
//
// It reads the tenant's audit rows newest first and filters by entity rather
// than adding an index — the "who touched this invoice" panel, opened one
// entity at a time by a human. entity_id is a filter, and DynamoDB applies
// Limit to the rows it reads before filtering, so the read follows the cursor
// until limit entries are found, the range ends, or the read budget above is
// spent. Past that budget (an entity whose last activity is more than ~10,000
// tenant audit rows ago) the trail can be incomplete; the fix for that is an
// entity index — never a Scan.
func (r *AuditRepository) ListForEntity(
	ctx context.Context,
	organizationID string,
	livemode bool,
	entityID string,
	limit int,
) ([]billing.AuditLog, error) {
	if limit <= 0 || limit > entityAuditMaxLimit {
		limit = entityAuditMaxLimit
	}
	var items []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for range entityAuditMaxQueries {
		// A fixed read size: Limit is also the rows DynamoDB evaluates per call,
		// so shrinking it to the matches still wanted would crawl a row at a time.
		res, err := r.base.Query(ctx, QueryOpts{
			PK:                TenantPK(organizationID, livemode),
			SKPrefix:          skAudit,
			FilterField:       "entity_id",
			FilterValue:       entityID,
			Limit:             max(limit, entityAuditReadSize),
			ScanIndexForward:  false,
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
	if len(items) > limit {
		items = items[:limit]
	}
	slices.Reverse(items) // read newest first, returned oldest first
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

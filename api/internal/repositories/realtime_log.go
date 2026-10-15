package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// The realtime change log (docs/specs/2026-10-14-realtime.md § 4.2).
//
// A Change is a hint to read, never data: ids and state only. It is written once
// per scope that may hear it, **in the same transaction as the mutation it
// describes**, so a mutation that commits always has its change and one that
// fails never leaves one. It is published to live connections after the commit
// (that can fail without failing anything), and a connection that missed the
// publish finds the change here when it reconnects with its cursor.
//
// The items live in the webhook table beside the events: same table, a partition
// of their own ("RT#{scope}"), the change's ULID as the sort key, 48 hours of TTL.

const (
	realtimePKPrefix = "RT#"

	// ChangeOverlap is how far before the cursor a replay starts. Two instances'
	// clocks can disagree by a few seconds, so a change written "after" the cursor
	// by the other instance can carry an older id; reading a little behind the
	// cursor hides none of them. The client drops the ones it already has by id.
	ChangeOverlap = 5 * time.Second

	// ChangeTTL is how long a change is kept; a cursor older than this is a gap.
	ChangeTTL = 48 * time.Hour
)

var (
	// ErrBadCursor is a cursor that is not a change id.
	ErrBadCursor = errors.New("realtime: malformed cursor")
	// ErrCursorTooOld is a cursor older than the log keeps: the client must reload
	// instead of replaying.
	ErrCursorTooOld = errors.New("realtime: cursor older than the change log")
)

// Change is one entry of the log. It carries ids and state words only.
type Change struct {
	// ID is a ULID and is the cursor. Assigned when the change is written if empty.
	ID       string
	Type     string // "invoice.paid": the webhook names, plus the realtime-only ones
	Entity   string // "invoice"
	EntityID string
	// Status is the entity's new status, when it has one.
	Status string
	// Revision orders changes of one entity: a client ignores one older than the
	// last it applied. Milliseconds since the epoch at the write, which the
	// conditional write of a transition serializes per entity.
	Revision   uint64
	OccurredAt time.Time
	// Scopes are the keys that may hear it (realtime.TenantScope and friends).
	Scopes []string
}

// changeRow is one scope's copy of a Change.
type changeRow struct {
	keys
	Scope        string `dynamodbav:"scope"`
	Type         string `dynamodbav:"type"`
	Entity       string `dynamodbav:"entity"`
	EntityID     string `dynamodbav:"entity_id"`
	Status       string `dynamodbav:"status,omitempty"`
	Revision     uint64 `dynamodbav:"revision"`
	OccurredAtMs uint64 `dynamodbav:"occurred_at_ms"`
}

func changeFromRow(r changeRow) Change {
	return Change{
		ID: r.SK, Type: r.Type, Entity: r.Entity, EntityID: r.EntityID, Status: r.Status,
		Revision: r.Revision, OccurredAt: time.UnixMilli(int64(r.OccurredAtMs)).UTC(),
		Scopes: []string{r.Scope},
	}
}

func changePK(scope string) string { return realtimePKPrefix + scope }

// changeWrites renders one item per scope, for a transaction. It fills in c.ID,
// c.Revision and c.OccurredAt when they are empty, in place, so the caller can
// publish the very same change after the commit. Nil when c is nil or has no
// scope (the log is off, or nobody may hear it).
func changeWrites(base Base, c *Change, now time.Time) ([]types.TransactWriteItem, error) {
	if c == nil || len(c.Scopes) == 0 {
		return nil, nil
	}
	if c.Type == "" || c.Entity == "" || c.EntityID == "" {
		return nil, fmt.Errorf("repositories: a change needs a type, an entity and an entity id (got %q %q %q)", c.Type, c.Entity, c.EntityID)
	}
	if c.ID == "" {
		c.ID = id.NewAt(now)
	}
	if c.OccurredAt.IsZero() {
		c.OccurredAt = now.UTC()
	}
	if c.Revision == 0 {
		c.Revision = uint64(c.OccurredAt.UnixMilli())
	}

	out := make([]types.TransactWriteItem, 0, len(c.Scopes))
	seen := map[string]bool{}
	for _, scope := range c.Scopes {
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		item, err := Encode(changeRow{
			keys:  newKeys(changePK(scope), c.ID, RetentionRealtimeChange, now),
			Scope: scope, Type: c.Type, Entity: c.Entity, EntityID: c.EntityID, Status: c.Status,
			Revision: c.Revision, OccurredAtMs: uint64(c.OccurredAt.UnixMilli()),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, base.BuildPutTxItemIfAbsent(item))
	}
	return out, nil
}

// RealtimeRepository reads the change log, and writes the changes that are not
// part of a transition (a customer edited, a payer recorded).
type RealtimeRepository struct {
	base Base
}

func NewRealtimeRepository(db *dynamodb.Client, cfg *config.Config) *RealtimeRepository {
	return &RealtimeRepository{base: NewBase(db, cfg, TableWebhooks)}
}

// Write stores a change on its own, outside any other transaction. For a change
// that has a transaction of its own, put it in the transition (StatusChange.Realtime).
func (r *RealtimeRepository) Write(ctx context.Context, c *Change, now time.Time) error {
	writes, err := changeWrites(r.base, c, now)
	if err != nil || len(writes) == 0 {
		return err
	}
	return r.base.TransactWrite(ctx, writes)
}

// CursorTime is the instant a cursor was minted.
func CursorTime(cursor string) (time.Time, error) {
	t, ok := id.Time(cursor)
	if !ok {
		return time.Time{}, ErrBadCursor
	}
	return t, nil
}

// CursorTooOld reports whether a replay from cursor is no longer possible at now.
// A margin of the overlap keeps a cursor right at the edge from half-replaying.
func CursorTooOld(cursor string, now time.Time) (bool, error) {
	t, err := CursorTime(cursor)
	if err != nil {
		return false, err
	}
	return now.Sub(t) > ChangeTTL-ChangeOverlap, nil
}

// Since returns the changes of the given scopes from ChangeOverlap before the
// cursor, in id order, deduplicated across scopes (one change written to three
// scopes is one change), at most limit of them; more says there are others.
//
// An empty cursor is a position, not a request: it returns nothing, and the
// caller learns where "now" is from Head.
func (r *RealtimeRepository) Since(ctx context.Context, scopes []string, cursor string, limit int) (changes []Change, more bool, err error) {
	if cursor == "" || limit <= 0 {
		return nil, false, nil
	}
	at, err := CursorTime(cursor)
	if err != nil {
		return nil, false, err
	}
	lower := id.FloorAt(at.Add(-ChangeOverlap))

	byID := map[string]Change{}
	for _, scope := range uniqueScopes(scopes) {
		out, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String(r.base.TableName),
			KeyConditionExpression:    aws.String("pk = :pk AND sk > :from"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: changePK(scope)}, ":from": &types.AttributeValueMemberS{Value: lower}},
			Limit:                     aws.Int32(int32(limit + 1)),
		})
		if err != nil {
			return nil, false, err
		}
		if len(out.Items) > limit {
			more = true
		}
		for _, item := range out.Items {
			var row changeRow
			if err := attributevalue.UnmarshalMap(item, &row); err != nil {
				return nil, false, err
			}
			c := changeFromRow(row)
			if prev, ok := byID[c.ID]; ok {
				c.Scopes = append(prev.Scopes, c.Scopes...)
			}
			byID[c.ID] = c
		}
	}

	changes = make([]Change, 0, len(byID))
	for _, c := range byID {
		changes = append(changes, c)
	}
	slices.SortFunc(changes, func(a, b Change) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	if len(changes) > limit {
		changes, more = changes[:limit], true
	}
	return changes, more, nil
}

// Head is the newest change id across the scopes, or "" when there is none.
func (r *RealtimeRepository) Head(ctx context.Context, scopes []string) (string, error) {
	head := ""
	for _, scope := range uniqueScopes(scopes) {
		out, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String(r.base.TableName),
			KeyConditionExpression:    aws.String("pk = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: changePK(scope)}},
			ScanIndexForward:          aws.Bool(false),
			Limit:                     aws.Int32(1),
			ProjectionExpression:      aws.String("sk"),
		})
		if err != nil {
			return "", err
		}
		for _, item := range out.Items {
			if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok && sk.Value > head {
				head = sk.Value
			}
		}
	}
	return head, nil
}

func uniqueScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

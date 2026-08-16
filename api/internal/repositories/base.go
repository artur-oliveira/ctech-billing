package repositories

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/dynamo"
	"gopkg.aoctech.app/billing/api/internal/config"
)

// The persistence primitives come from api-commons rather than being written
// again here. Re-implementing transactional writes, composite-key queries and
// atomic counters per service is the cross-stack duplication the company-wide
// audit already recorded costing a real divergence.

type (
	// Base provides the shared DynamoDB operations.
	Base = dynamo.Base
	// QueryOpts configures a Query.
	QueryOpts = dynamo.QueryOpts
	// CompositeQueryOpts configures a multi-attribute (composite) key query —
	// what the period index is built for.
	CompositeQueryOpts = dynamo.CompositeQueryOpts
	// KV is one equality condition on a composite sort-key attribute.
	KV = dynamo.KV
	// QueryResult is a raw page of items plus the continuation key.
	QueryResult = dynamo.QueryResult
)

var (
	// Encode marshals a value into DynamoDB attribute values, omitting nulls.
	Encode = dynamo.Encode
	// IsConditionFailed reports a conditional-check failure, whether it came
	// from a single write or from inside a cancelled transaction.
	IsConditionFailed = dynamo.IsConditionFailed
)

// txItems is sugar for composing a transaction, so call sites read as a list of
// writes rather than a slice literal with a type name in it.
func txItems(items ...types.TransactWriteItem) []types.TransactWriteItem { return items }

// Page is a decoded, tenant-scoped page. Handlers never see raw attribute maps.
type Page[T any] struct {
	Items            []T
	LastEvaluatedKey map[string]types.AttributeValue
}

// PhysicalName returns the deployed name of a logical table: {prefix}_{table}.
func PhysicalName(tablePrefix, logical string) string {
	return dynamo.TableName(tablePrefix, logical)
}

// TableName returns the environment-prefixed physical name of one logical table.
func TableName(cfg *config.Config, logical string) string {
	return PhysicalName(cfg.TablePrefix, logical)
}

// NewBase binds a Base to one logical table.
//
// Every repository takes its own, and several take two: a status change and the
// audit row that records it are written together, and the audit row lives in the
// audit table. A TransactWriteItem carries its own table name, so one
// TransactWriteItems call spans both — the atomicity that guarantee rests on is
// DynamoDB's, not a property of sharing a table.
func NewBase(db *dynamodb.Client, cfg *config.Config, logical string) Base {
	return dynamo.NewBase(db, cfg.TablePrefix, logical)
}

// Decode unmarshals one item.
func Decode[T any](item map[string]types.AttributeValue) (*T, error) {
	return dynamo.Decode[T](item)
}

// DecodeItems unmarshals a page, preserving order. A malformed item fails the
// whole read rather than returning partial data — a half-decoded page of
// invoices is worse than an error, because it looks like an answer.
func DecodeItems[T any](items []map[string]types.AttributeValue) ([]T, error) {
	out := make([]T, 0, len(items))
	for _, item := range items {
		decoded, err := Decode[T](item)
		if err != nil {
			return nil, err
		}
		out = append(out, *decoded)
	}
	return out, nil
}

// EncodeCursor renders a page's continuation key as an opaque string.
//
// Every key attribute in every table is a string (schema.json), so the key is a
// small string map and the cursor is that map, base64'd. It is opaque on
// purpose: a client that parses it starts depending on the key layout, and the
// key layout has to stay free to change.
func EncodeCursor(key map[string]types.AttributeValue) string {
	if len(key) == 0 {
		return ""
	}
	plain := make(map[string]string, len(key))
	for k, v := range key {
		s, ok := v.(*types.AttributeValueMemberS)
		if !ok {
			// Unreachable while every key attribute is a string. If that ever
			// changes, no cursor is better than a truncated one that silently
			// restarts the listing from the top.
			return ""
		}
		plain[k] = s.Value
	}
	raw, err := json.Marshal(plain)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses a cursor back into a continuation key. An empty cursor
// means "from the beginning", which is not an error.
func DecodeCursor(cursor string) (map[string]types.AttributeValue, error) {
	if cursor == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	var plain map[string]string
	if err := json.Unmarshal(raw, &plain); err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	key := make(map[string]types.AttributeValue, len(plain))
	for k, v := range plain {
		key[k] = &types.AttributeValueMemberS{Value: v}
	}
	return key, nil
}

// pagePeriod queries the period index for one entity type and returns a decoded
// page of the domain values.
//
// Every tenant listing in this package is the same query with a different entity
// and a different row type, and four copies of it is four places to forget the
// index name or the prefix helper. skPrefix is empty for "everything", or a
// PeriodPrefix for a year or a month.
func pagePeriod[Row any, T any](
	ctx context.Context,
	base Base,
	organizationID string,
	livemode bool,
	entity Entity,
	skPrefix string,
	limit int,
	startKey map[string]types.AttributeValue,
	pick func(Row) T,
) (*Page[T], error) {
	res, err := base.Query(ctx, QueryOpts{
		IndexName:         IndexPeriod,
		PKField:           "period_pk",
		SKField:           "period_sk",
		PK:                PeriodPK(organizationID, livemode, entity),
		SKPrefix:          skPrefix,
		Limit:             limit,
		ExclusiveStartKey: startKey,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[Row](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]T, len(rows))
	for i, row := range rows {
		out[i] = pick(row)
	}
	return &Page[T]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// DecodePage decodes a QueryResult into a typed page.
func DecodePage[T any](result *QueryResult) (*Page[T], error) {
	items, err := DecodeItems[T](result.Items)
	if err != nil {
		return nil, err
	}
	return &Page[T]{Items: items, LastEvaluatedKey: result.LastEvaluatedKey}, nil
}

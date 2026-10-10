package repositories

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

var (
	// ErrDuplicateLevel is a retried report: the same key with the same body.
	// Callers answer success.
	ErrDuplicateLevel = errors.New("level already reported")
	// ErrLevelKeyReused is the same key with another body: a caller bug that
	// must not become a silent second level.
	ErrLevelKeyReused = errors.New("idempotency key already used for another level")
)

const levelKeySK = "KEY"

const levelLatestSK = "LATEST"

// LevelRepository stores level reports (spec § 6.2) in the usage table.
type LevelRepository struct {
	base  Base
	table string // physical name, for the one conditional Put Base has no builder for
}

func NewLevelRepository(db *dynamodb.Client, cfg *config.Config) *LevelRepository {
	return &LevelRepository{base: NewBase(db, cfg, TableUsage), table: TableName(cfg, TableUsage)}
}

// levelLatestRow is the newest level of one (customer_ref, meter). No TTL.
type levelLatestRow struct {
	keys
	billing.LevelRecord
	At string `dynamodbav:"at"` // levelBound(OccurredAt), compared in the condition
}

type levelRow struct {
	keys
	billing.LevelRecord
}

type levelKeyRow struct {
	keys
	Hash string `dynamodbav:"hash"`
}

func levelHash(l *billing.LevelRecord) string {
	h := sha256.Sum256([]byte(l.CustomerRef + "\x00" + l.Meter + "\x00" + strconv.FormatInt(l.Value, 10) + "\x00" + levelBound(l.OccurredAt)))
	return hex.EncodeToString(h[:])
}

// Append stores a report once per idempotency key: the level, its key marker
// and — when it is the newest level of its meter — the no-TTL latest item, in
// one transaction (scope decisions 8 and 9). The latest is conditional on still
// being the one read; when only that condition fails, another report moved it
// first, and the whole report is re-planned (at most 3 times).
func (r *LevelRepository) Append(ctx context.Context, l *billing.LevelRecord, now time.Time) error {
	if err := l.Validate(); err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		latest, err := r.latestRow(ctx, l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter)
		if err != nil {
			return err
		}
		rec := *l
		newest := latest == nil || !l.OccurredAt.Before(latest.OccurredAt)
		switch {
		case newest && latest != nil:
			rec.Previous = latest.Value
		case !newest:
			// A late report: what came before it is whatever is stored before it.
			prior, err := r.LatestBefore(ctx, l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter, l.OccurredAt)
			if err != nil {
				return err
			}
			if prior != nil {
				rec.Previous = prior.Value
			}
		}
		items, err := r.reportItems(&rec, now)
		if err != nil {
			return err
		}
		if newest {
			put, err := r.latestPut(&rec, latest, now)
			if err != nil {
				return err
			}
			items = append(items, put)
		}
		err = r.base.TransactWrite(ctx, items)
		codes := cancellationCodes(err)
		if newest && len(codes) == 3 && codes[0] == codeNone && codes[1] == codeNone && codes[2] == codeConditionFailed {
			continue // the latest moved under us: re-read and re-plan
		}
		if !IsConditionFailed(err) {
			return conflictErr(err)
		}
		return r.duplicateOrReused(ctx, l)
	}
	return fmt.Errorf("%w: the latest level of %s kept moving", ErrTransactionConflict, l.Meter)
}

// reportItems are the marker and the report, both put-if-absent.
func (r *LevelRepository) reportItems(l *billing.LevelRecord, now time.Time) ([]types.TransactWriteItem, error) {
	item, err := Encode(levelRow{
		keys:        newKeys(LevelPK(l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter), LevelSK(l.OccurredAt, l.IdempotencyKey), RetentionLevel, now),
		LevelRecord: *l,
	})
	if err != nil {
		return nil, err
	}
	marker, err := Encode(levelKeyRow{
		keys: newKeys(LevelKeyPK(l.OrganizationID, l.Livemode, l.IdempotencyKey), levelKeySK, RetentionLevel, now),
		Hash: levelHash(l),
	})
	if err != nil {
		return nil, err
	}
	return txItems(r.base.BuildPutTxItemIfAbsent(marker), r.base.BuildPutTxItemIfAbsent(item)), nil
}

// latestPut replaces the latest item only if it is still the one read.
func (r *LevelRepository) latestPut(l *billing.LevelRecord, read *levelLatestRow, now time.Time) (types.TransactWriteItem, error) {
	item, err := Encode(levelLatestRow{
		keys:        newKeys(LevelLatestPK(l.OrganizationID, l.Livemode, l.CustomerRef, l.Meter), levelLatestSK, RetentionPermanent, now),
		LevelRecord: *l,
		At:          levelBound(l.OccurredAt),
	})
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	put := &types.Put{TableName: aws.String(r.table), Item: item}
	if read == nil {
		put.ConditionExpression = aws.String("attribute_not_exists(pk)")
	} else {
		put.ConditionExpression = aws.String("#at = :read")
		put.ExpressionAttributeNames = map[string]string{"#at": "at"}
		put.ExpressionAttributeValues = map[string]types.AttributeValue{":read": &types.AttributeValueMemberS{Value: read.At}}
	}
	return types.TransactWriteItem{Put: put}, nil
}

// duplicateOrReused reads the marker after a refused report.
func (r *LevelRepository) duplicateOrReused(ctx context.Context, l *billing.LevelRecord) error {
	stored, err := r.base.GetItem(ctx, LevelKeyPK(l.OrganizationID, l.Livemode, l.IdempotencyKey), levelKeySK)
	if err != nil {
		return err
	}
	if stored == nil {
		// The level item itself existed with no marker: the same instant and key,
		// so the same report.
		return fmt.Errorf("%w: %s", ErrDuplicateLevel, l.IdempotencyKey)
	}
	row, err := Decode[levelKeyRow](stored)
	if err != nil {
		return err
	}
	if row.Hash != levelHash(l) {
		return fmt.Errorf("%w: %s", ErrLevelKeyReused, l.IdempotencyKey)
	}
	return fmt.Errorf("%w: %s", ErrDuplicateLevel, l.IdempotencyKey)
}

func (r *LevelRepository) latestRow(ctx context.Context, organizationID string, livemode bool, customerRef, meter string) (*levelLatestRow, error) {
	item, err := r.base.GetItem(ctx, LevelLatestPK(organizationID, livemode, customerRef, meter), levelLatestSK)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[levelLatestRow](item)
}

// Latest is the newest level of (customer_ref, meter), kept with no TTL.
func (r *LevelRepository) Latest(ctx context.Context, organizationID string, livemode bool, customerRef, meter string) (*billing.LevelRecord, error) {
	row, err := r.latestRow(ctx, organizationID, livemode, customerRef, meter)
	if err != nil || row == nil {
		return nil, err
	}
	return &row.LevelRecord, nil
}

// FirstFrom is the oldest in-TTL report at or after from.
func (r *LevelRepository) FirstFrom(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, from time.Time) (*billing.LevelRecord, error) {
	res, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("pk = :pk AND sk >= :from"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":   &types.AttributeValueMemberS{Value: LevelPK(organizationID, livemode, customerRef, meter)},
			":from": &types.AttributeValueMemberS{Value: levelBound(from)},
		},
		ScanIndexForward: aws.Bool(true),
		Limit:            aws.Int32(1),
		ConsistentRead:   aws.Bool(true),
	})
	if err != nil || len(res.Items) == 0 {
		return nil, err
	}
	row, err := Decode[levelRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.LevelRecord, nil
}

// LatestBefore is the last level reached strictly before `before`: one Query,
// newest first, limit 1. Nil when there is none.
func (r *LevelRepository) LatestBefore(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, before time.Time) (*billing.LevelRecord, error) {
	res, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("pk = :pk AND sk < :before"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: LevelPK(organizationID, livemode, customerRef, meter)},
			":before": &types.AttributeValueMemberS{Value: levelBound(before)},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(1),
		ConsistentRead:   aws.Bool(true),
	})
	if err != nil || len(res.Items) == 0 {
		return nil, err
	}
	row, err := Decode[levelRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.LevelRecord, nil
}

// InPeriod returns the levels reached in [from, to), oldest first, following
// continuation keys: the close needs all of them or none.
func (r *LevelRepository) InPeriod(ctx context.Context, organizationID string, livemode bool, customerRef, meter string, from, to time.Time) ([]billing.LevelRecord, error) {
	var out []billing.LevelRecord
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.QueryRaw(ctx, &dynamodb.QueryInput{
			// BETWEEN is inclusive, and a report at exactly `to` sorts after
			// levelBound(to) ("…Z#key" > "…Z"), so this is [from, to).
			KeyConditionExpression: aws.String("pk = :pk AND sk BETWEEN :lo AND :hi"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: LevelPK(organizationID, livemode, customerRef, meter)},
				":lo": &types.AttributeValueMemberS{Value: levelBound(from)},
				":hi": &types.AttributeValueMemberS{Value: levelBound(to)},
			},
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[levelRow](res.Items)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, row.LevelRecord)
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

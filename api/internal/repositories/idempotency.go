package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
)

// ErrIdempotencyConflict reports a key reused with a different request body.
//
// It is not the same as a replay: a replay returns the stored response. This is
// a caller reusing a key for a different operation, which almost always means a
// bug in their key generation — and answering it with the first request's
// response would silently confirm an operation that never happened.
var ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")

// IdempotencyTTL is how long a recorded response is replayable.
//
// 24 hours: long enough to cover any retry a sane client performs, short enough
// that the store does not become a second copy of the request log.
const IdempotencyTTL = 24 * time.Hour

// IdempotencyRecord is a completed request, keyed by the caller's key.
type IdempotencyRecord struct {
	Key         string `dynamodbav:"idem_key"     json:"key"`
	RequestHash string `dynamodbav:"request_hash" json:"request_hash"`
	Status      int    `dynamodbav:"status"       json:"status"`
	Response    string `dynamodbav:"response"     json:"response"`
	Route       string `dynamodbav:"route"        json:"route"`
}

type idempotencyRow struct {
	keys
	IdempotencyRecord
}

// IdempotencyRepository stores completed responses for replay.
//
// It is tenant-scoped: two organizations using the same key value are two
// different requests, and sharing the namespace would let one merchant's retry
// return another's response.
type IdempotencyRepository struct {
	base Base
}

func NewIdempotencyRepository(db *dynamodb.Client, cfg *config.Config) *IdempotencyRepository {
	return &IdempotencyRepository{base: NewBase(db, cfg, TableIdempotency)}
}

// ErrIdempotencyInProgress: another request with the same key holds the claim
// and has not answered yet. The caller retries after a moment and gets its
// answer replayed.
var ErrIdempotencyInProgress = errors.New("a request with this idempotency key is still running")

// IdempotencyLease is how long a claim holds without an answer: past it, a
// process that died mid-request no longer blocks the key. Longer than any
// request runs (the bank calls time out well inside it).
const IdempotencyLease = 2 * time.Minute

// Claim reserves the key for this request before it runs, in one conditional
// write, so two processes racing with one key cannot both run the operation.
// It answers the stored record when the key already completed (a replay),
// ErrIdempotencyConflict for another body, ErrIdempotencyInProgress while the
// first request still runs, and (nil, nil) when the caller holds the claim.
func (r *IdempotencyRepository) Claim(ctx context.Context, owner string, livemode bool, key, requestHash, route string, now time.Time) (*IdempotencyRecord, error) {
	item, err := Encode(idempotencyRow{
		keys: keys{
			PK:        TenantPK(owner, livemode),
			SK:        IdempotencySK(key),
			TTL:       new(now.Add(IdempotencyLease).Unix()),
			CreatedAt: now.UTC().Format(time.RFC3339Nano),
			UpdatedAt: now.UTC().Format(time.RFC3339Nano),
		},
		IdempotencyRecord: IdempotencyRecord{Key: key, RequestHash: requestHash, Route: route},
	})
	if err != nil {
		return nil, err
	}
	// Free, or a claim whose holder died (status 0 past its lease).
	_, err = r.base.PutItemRaw(ctx, &dynamodb.PutItemInput{
		Item:                     item,
		ConditionExpression:      aws.String("attribute_not_exists(pk) OR (#s = :zero AND #ttl < :now)"),
		ExpressionAttributeNames: map[string]string{"#s": "status", "#ttl": "ttl"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":zero": &types.AttributeValueMemberN{Value: "0"},
			":now":  &types.AttributeValueMemberN{Value: strconv.FormatInt(now.Unix(), 10)},
		},
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err == nil {
		return nil, nil
	}
	var ccf *types.ConditionalCheckFailedException
	if !errors.As(err, &ccf) || ccf.Item == nil {
		return nil, err
	}
	row, derr := Decode[idempotencyRow](ccf.Item)
	switch {
	case derr != nil:
		return nil, derr
	case row.RequestHash != requestHash:
		return nil, fmt.Errorf("%w: %s", ErrIdempotencyConflict, key)
	case row.Status == 0:
		return nil, fmt.Errorf("%w: %s", ErrIdempotencyInProgress, key)
	}
	return &row.IdempotencyRecord, nil
}

// Complete turns the caller's claim into the replayable answer (24 hours).
func (r *IdempotencyRepository) Complete(ctx context.Context, owner string, livemode bool, rec IdempotencyRecord, now time.Time) error {
	item, err := Encode(idempotencyRow{
		keys: keys{
			PK:        TenantPK(owner, livemode),
			SK:        IdempotencySK(rec.Key),
			TTL:       new(now.Add(IdempotencyTTL).Unix()),
			CreatedAt: now.UTC().Format(time.RFC3339Nano),
			UpdatedAt: now.UTC().Format(time.RFC3339Nano),
		},
		IdempotencyRecord: rec,
	})
	if err != nil {
		return err
	}
	return r.base.PutItem(ctx, item)
}

// Release gives up the caller's claim (the request failed: a 4xx or 5xx is not
// replayed, so the fixed retry runs). Only a claim is removed, never an answer.
func (r *IdempotencyRepository) Release(ctx context.Context, owner string, livemode bool, key string) error {
	_, err := r.base.DeleteItemRaw(ctx, &dynamodb.DeleteItemInput{
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: TenantPK(owner, livemode)},
			"sk": &types.AttributeValueMemberS{Value: IdempotencySK(key)},
		},
		ConditionExpression:       aws.String("#s = :zero"),
		ExpressionAttributeNames:  map[string]string{"#s": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":zero": &types.AttributeValueMemberN{Value: "0"}},
	})
	if IsConditionFailed(err) {
		return nil
	}
	return err
}

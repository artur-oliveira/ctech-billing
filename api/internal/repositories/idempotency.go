package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

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

// Lookup returns a previously stored response for the key.
//
// A key found with a different request hash returns ErrIdempotencyConflict; a
// key not found returns (nil, nil), which is the signal to run the operation.
func (r *IdempotencyRepository) Lookup(ctx context.Context, organizationID string, livemode bool, key, requestHash string) (*IdempotencyRecord, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), IdempotencySK(key))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	row, err := Decode[idempotencyRow](item)
	if err != nil {
		return nil, err
	}
	if row.RequestHash != requestHash {
		return nil, fmt.Errorf("%w: %s", ErrIdempotencyConflict, key)
	}
	return &row.IdempotencyRecord, nil
}

// Store records a completed response.
//
// The write is create-only. Two concurrent requests with the same key both run
// the operation — this store does not lock, and pretending otherwise would be
// worse than not having it — but only the first result is recorded, so every
// later replay returns one consistent answer rather than whichever finished
// last. Real mutual exclusion belongs in the operation's own conditional write,
// which is where it already is for invoices and usage.
func (r *IdempotencyRepository) Store(ctx context.Context, organizationID string, livemode bool, rec IdempotencyRecord, now time.Time) error {
	expires := now.Add(IdempotencyTTL).Unix()
	item, err := Encode(idempotencyRow{
		keys: keys{
			PK:        TenantPK(organizationID, livemode),
			SK:        IdempotencySK(rec.Key),
			TTL:       &expires,
			CreatedAt: now.UTC().Format(time.RFC3339Nano),
			UpdatedAt: now.UTC().Format(time.RFC3339Nano),
		},
		IdempotencyRecord: rec,
	})
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		// Another in-flight request with the same key won the race. Its response
		// is the one that will be replayed; ours is discarded, not an error.
		return nil
	}
	return err
}

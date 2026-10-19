package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// scheduleEntry is one parsed schedule-index row.
type scheduleEntry struct {
	Space space.ResolvedSpace
	ID    string
}

// scanSchedule reads the date-ordered work list for one job: every row whose
// schedule date is on or before today, oldest first, up to limit. Rows whose
// owner does not rebuild a valid space are counted in skipped, never returned.
//
// The index is eventually consistent and a row can be stale, so callers treat
// the list as a to-do list and re-read the row (and re-check its state inside
// the conditional write) before acting.
func scanSchedule(ctx context.Context, b Base, schedulePK string, livemode bool, today brcal.Date, limit int) (entries []scheduleEntry, skipped int, err error) {
	var start map[string]types.AttributeValue
	for len(entries) < limit {
		page, sk, next, perr := scanSchedulePage(ctx, b, schedulePK, livemode, today, limit, start)
		if perr != nil {
			return nil, skipped, perr
		}
		entries, skipped = append(entries, page...), skipped+sk
		if next == nil {
			break
		}
		start = next
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, skipped, nil
}

// scanSchedulePage is one query of the work list: up to limit index rows from
// start, and the key to continue from (nil at the end of the list). A job that
// must see the whole list pages with it, so a row that keeps failing is passed
// over instead of being the head of every read. The continuation key stays valid
// while the rows before it leave the index, which is what working through the
// list does.
func scanSchedulePage(ctx context.Context, b Base, schedulePK string, livemode bool, today brcal.Date, limit int, start map[string]types.AttributeValue) (entries []scheduleEntry, skipped int, next map[string]types.AttributeValue, err error) {
	out, qerr := b.QueryRaw(ctx, &dynamodb.QueryInput{
		IndexName:              aws.String(IndexSchedule),
		KeyConditionExpression: aws.String("schedule_pk = :pk AND schedule_sk BETWEEN :lo AND :hi"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": str(schedulePK),
			":lo": str("0"),
			// "~" sorts after every character an owner or id uses, so every row
			// of today is included.
			":hi": str(today.String() + "#~"),
		},
		Limit:             aws.Int32(int32(limit)),
		ExclusiveStartKey: start,
	})
	if qerr != nil {
		return nil, 0, nil, qerr
	}
	for _, item := range out.Items {
		skAttr, ok := item["schedule_sk"].(*types.AttributeValueMemberS)
		if !ok {
			skipped++
			continue
		}
		_, owner, id, perr := ParseScheduleSK(skAttr.Value)
		if perr != nil {
			skipped++
			continue
		}
		// The ONE call to space.ForJob in internal/: the daily job has no request to
		// resolve a space from, so it rebuilds each row's space from the owner in its
		// schedule key. ForJob validates the owner; a forged or malformed one is
		// counted as skipped, never trusted. (TestForJobIsNotCalledFromInternal names
		// this file; the call is inlined so no other file here can reuse it.)
		sp, serr := space.ForJob(owner, livemode)
		if serr != nil {
			skipped++
			continue
		}
		entries = append(entries, scheduleEntry{Space: sp, ID: id})
	}
	if len(out.LastEvaluatedKey) > 0 {
		next = out.LastEvaluatedKey
	}
	return entries, skipped, next, nil
}

func (e scheduleEntry) String() string { return fmt.Sprintf("%s/%s", e.Space.PK(), e.ID) }

// batchGetEntries reads the row of every entry in as few calls as DynamoDB
// allows (100 keys each), in the order of entries; a row that is gone is nil.
// It is the hydration of a due list: a page of up to a few hundred rows used to
// cost one GetItem round trip each. Eventually consistent, as the GetItem it
// replaces; unprocessed keys are retried a few times, then the page fails whole.
func batchGetEntries(ctx context.Context, b Base, entries []scheduleEntry, sk func(id string) string) ([]map[string]types.AttributeValue, error) {
	const perCall, attempts = 100, 6
	rows := make(map[string]map[string]types.AttributeValue, len(entries))
	keyOf := func(pk, sk string) string { return pk + "\x00" + sk }
	for lo := 0; lo < len(entries); lo += perCall {
		var keys []map[string]types.AttributeValue
		seen := map[string]bool{}
		for _, e := range entries[lo:min(lo+perCall, len(entries))] {
			k := keyOf(e.Space.PK(), sk(e.ID))
			if !seen[k] {
				seen[k] = true
				keys = append(keys, map[string]types.AttributeValue{"pk": str(e.Space.PK()), "sk": str(sk(e.ID))})
			}
		}
		req := map[string]types.KeysAndAttributes{b.TableName: {Keys: keys}}
		for attempt := 0; len(req) > 0; attempt++ {
			if attempt == attempts {
				return nil, fmt.Errorf("%s: %d keys still unprocessed after retries", b.TableName, len(req[b.TableName].Keys))
			}
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
				}
			}
			res, err := b.BatchGetItemRaw(ctx, &dynamodb.BatchGetItemInput{RequestItems: req})
			if err != nil {
				return nil, err
			}
			for _, item := range res.Responses[b.TableName] {
				pk, _ := item["pk"].(*types.AttributeValueMemberS)
				k, _ := item["sk"].(*types.AttributeValueMemberS)
				if pk != nil && k != nil {
					rows[keyOf(pk.Value, k.Value)] = item
				}
			}
			req = res.UnprocessedKeys
		}
	}
	out := make([]map[string]types.AttributeValue, len(entries))
	for i, e := range entries {
		out[i] = rows[keyOf(e.Space.PK(), sk(e.ID))]
	}
	return out, nil
}

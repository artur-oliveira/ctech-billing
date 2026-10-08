package repositories

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// spaceFromScheduleOwner rebuilds a row's space from the owner stored in its
// schedule key. It is the ONE place in internal/ that calls space.ForJob
// (TestForJobIsNotCalledFromInternal names this file), because the daily job
// reads a cross-tenant index and has no request to resolve a space from. The
// owner is validated by ForJob: a forged or malformed one is refused, never
// trusted.
func spaceFromScheduleOwner(owner string, livemode bool) (space.ResolvedSpace, error) {
	return space.ForJob(owner, livemode)
}

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
			return nil, skipped, qerr
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
			sp, serr := spaceFromScheduleOwner(owner, livemode)
			if serr != nil {
				skipped++
				continue
			}
			entries = append(entries, scheduleEntry{Space: sp, ID: id})
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, skipped, nil
}

func (e scheduleEntry) String() string { return fmt.Sprintf("%s/%s", e.Space.PK(), e.ID) }

package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type row struct{ space, id string }

func spaceOfRow(r row) string { return r.space }
func idOfRow(r row) string    { return r.id }

// paged serves rows in pages of size n, with a continuation key that is the index
// of the next row.
func paged(rows []row, n int) func(map[string]types.AttributeValue) ([]row, int, map[string]types.AttributeValue, error) {
	return func(start map[string]types.AttributeValue) ([]row, int, map[string]types.AttributeValue, error) {
		from := 0
		if start != nil {
			fmt.Sscanf(start["i"].(*types.AttributeValueMemberS).Value, "%d", &from)
		}
		to := min(from+n, len(rows))
		var next map[string]types.AttributeValue
		if to < len(rows) {
			next = map[string]types.AttributeValue{"i": &types.AttributeValueMemberS{Value: fmt.Sprint(to)}}
		}
		return rows[from:to], 0, next, nil
	}
}

func many(n int) []row {
	out := make([]row, n)
	for i := range out {
		out[i] = row{space: fmt.Sprintf("s%d", i), id: fmt.Sprintf("r%d", i)}
	}
	return out
}

// The whole list is worked, however many pages it takes, and each row once: the
// old job read 5000 rows and stopped.
func TestRunDueWorksEveryPageOfTheListOnce(t *testing.T) {
	for _, workers := range []int{1, 4} {
		var mu sync.Mutex
		seen := map[string]int{}
		var res JobResult
		runDue(context.Background(), workers, &res, "test", spaceOfRow, idOfRow, paged(many(23), 5), func(r row, local *JobResult) {
			mu.Lock()
			seen[r.id]++
			mu.Unlock()
			local.Done++
		})
		if res.Examined != 23 || res.Done != 23 || len(seen) != 23 {
			t.Fatalf("workers=%d: %+v, %d distinct rows, want 23", workers, res, len(seen))
		}
		for id, n := range seen {
			if n != 1 {
				t.Fatalf("workers=%d: row %s worked %d times", workers, id, n)
			}
		}
	}
}

// A row that comes back into the list while the run works it (a recurrence moved
// to a later date that is still due) is worked once per run.
func TestRunDueWorksARowThatComesBackOnlyOnce(t *testing.T) {
	rows := append(many(4), many(4)...) // every row appears again later in the list
	for _, workers := range []int{1, 3} {
		var worked atomic.Int32
		var res JobResult
		runDue(context.Background(), workers, &res, "test", spaceOfRow, idOfRow, paged(rows, 3), func(row, *JobResult) { worked.Add(1) })
		if worked.Load() != 4 || res.Examined != 4 {
			t.Fatalf("workers=%d: worked %d (examined %d), want 4", workers, worked.Load(), res.Examined)
		}
	}
}

// Rows of one space are never worked at the same time (they post to the same
// ledger rows), while different spaces overlap.
func TestRunDueNeverWorksTwoRowsOfOneSpaceAtOnce(t *testing.T) {
	var rows []row
	for i := 0; i < 12; i++ {
		rows = append(rows, row{space: "same", id: fmt.Sprintf("a%d", i)}, row{space: fmt.Sprintf("other%d", i), id: fmt.Sprintf("b%d", i)})
	}
	var inSame, maxSame, inAll, maxAll atomic.Int32
	var res JobResult
	runDue(context.Background(), 8, &res, "test", spaceOfRow, idOfRow, paged(rows, 4), func(r row, _ *JobResult) {
		now := inAll.Add(1)
		for {
			m := maxAll.Load()
			if now <= m || maxAll.CompareAndSwap(m, now) {
				break
			}
		}
		if r.space == "same" {
			n := inSame.Add(1)
			for {
				m := maxSame.Load()
				if n <= m || maxSame.CompareAndSwap(m, n) {
					break
				}
			}
		}
		time.Sleep(15 * time.Millisecond)
		if r.space == "same" {
			inSame.Add(-1)
		}
		inAll.Add(-1)
	})
	if maxSame.Load() != 1 {
		t.Fatalf("%d rows of one space were worked at once, want 1", maxSame.Load())
	}
	if maxAll.Load() < 2 {
		t.Fatalf("different spaces never overlapped (max %d at once)", maxAll.Load())
	}
}

// A failing read ends the walk and says so; what was read before it is still worked.
func TestRunDueReportsAFailedReadAndKeepsTheWorkDone(t *testing.T) {
	good := paged(many(4), 2)
	calls := 0
	page := func(start map[string]types.AttributeValue) ([]row, int, map[string]types.AttributeValue, error) {
		calls++
		if calls == 2 {
			return nil, 0, nil, errors.New("dynamodb down")
		}
		return good(start)
	}
	for _, workers := range []int{1, 4} {
		calls = 0
		var res JobResult
		runDue(context.Background(), workers, &res, "test", spaceOfRow, idOfRow, page, func(_ row, local *JobResult) { local.Done++ })
		if res.Done != 2 || res.Failed != 1 || len(res.Errors) != 1 {
			t.Fatalf("workers=%d: %+v, want 2 done and 1 failed read", workers, res)
		}
	}
}

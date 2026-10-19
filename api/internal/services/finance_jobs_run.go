package services

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// jobPage is how many rows of a work list one read asks for.
const jobPage = 500

// WithConcurrency sets how many rows of a work list the daily jobs work at once.
// The default is 1: serial, as the jobs always were. Rows of one space are always
// worked one at a time (they post to the same ledger rows), so what the pool adds
// is that the spaces of different people proceed together.
func (j *FinanceJobs) WithConcurrency(workers int) *FinanceJobs {
	j.workers = max(workers, 1)
	return j
}

func (r *JobResult) add(o JobResult) {
	r.Examined += o.Examined
	r.Done += o.Done
	r.Skipped += o.Skipped
	r.Failed += o.Failed
	r.Errors = append(r.Errors, o.Errors...)
}

// runDue works a whole due list. A reader pages it with the continuation key and
// hands each row to a pool; the list is not cut at a batch size, so a day that
// holds more rows than one read still drains in the run, and a row that keeps
// failing is passed over rather than being the head of every read.
//
// A row can come back into the list while the run works it (a recurrence moved to
// a later date that is still due): it is worked once per run, because
// those are bounded per run on purpose (maxDraftsPerRun).
func runDue[T any](ctx context.Context, workers int, res *JobResult, what string, spaceOf, idOf func(T) string,
	page func(start map[string]types.AttributeValue) ([]T, int, map[string]types.AttributeValue, error), work func(T, *JobResult)) {
	if workers <= 1 {
		runDueSerial(ctx, res, what, spaceOf, idOf, page, work)
		return
	}
	var mu sync.Mutex
	merge := func(o JobResult) {
		mu.Lock()
		res.add(o)
		mu.Unlock()
	}

	limit := newOrgLimiter(1) // one row of a space at a time
	rows := make(chan T)
	var wg sync.WaitGroup
	for i := 0; i < max(workers, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for row := range rows {
				release := limit.acquire(spaceOf(row))
				var local JobResult
				local.Examined++
				work(row, &local)
				release()
				merge(local)
			}
		}()
	}

	seen := map[string]bool{}
	var start map[string]types.AttributeValue
produce:
	for {
		items, skipped, next, err := page(start)
		if skipped > 0 {
			merge(JobResult{Skipped: skipped})
		}
		if err != nil {
			var local JobResult
			local.fail("reading the %s list: %v", what, err)
			merge(local)
			break
		}
		for _, it := range items {
			key := spaceOf(it) + "/" + idOf(it)
			if seen[key] {
				continue
			}
			seen[key] = true
			select {
			case rows <- it:
			case <-ctx.Done():
				var local JobResult
				local.fail("working the %s list was interrupted: %v", what, ctx.Err())
				merge(local)
				break produce
			}
		}
		if next == nil {
			break
		}
		start = next
	}
	close(rows)
	wg.Wait()
}

// runDueSerial is runDue with one worker: the list is read a page, worked, and the
// next page read, all on the caller's goroutine, which is how the jobs always ran.
func runDueSerial[T any](ctx context.Context, res *JobResult, what string, spaceOf, idOf func(T) string,
	page func(start map[string]types.AttributeValue) ([]T, int, map[string]types.AttributeValue, error), work func(T, *JobResult)) {
	seen := map[string]bool{}
	var start map[string]types.AttributeValue
	for {
		items, skipped, next, err := page(start)
		res.Skipped += skipped
		if err != nil {
			res.fail("reading the %s list: %v", what, err)
			return
		}
		for _, it := range items {
			if ctx.Err() != nil {
				res.fail("working the %s list was interrupted: %v", what, ctx.Err())
				return
			}
			key := spaceOf(it) + "/" + idOf(it)
			if seen[key] {
				continue
			}
			seen[key] = true
			res.Examined++
			work(it, res)
		}
		if next == nil {
			return
		}
		start = next
	}
}

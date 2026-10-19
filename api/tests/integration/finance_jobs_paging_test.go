//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The materialise list is read a page at a time with its continuation key. Rows that
// stay in the list (a recurrence that keeps failing is never moved) do not hide the
// rows behind them: the walk reaches every one, each once.
func TestTheMaterialiseListIsWalkedPageByPageToItsEnd(t *testing.T) {
	f := newBillsFixture(t)
	ours := map[string]bool{}
	var recs *repositories.RecurrenceRepository
	for day := 1; day <= 7; day++ {
		var rec finance.Recurrence
		rec, recs = f.recurrence(t, day, false)
		ours[rec.ID] = true
	}
	today := brcal.New(2026, 3, 20)

	seen := map[string]int{}
	var start map[string]types.AttributeValue
	pages := 0
	for {
		due, _, next, err := recs.DueToMaterialisePage(context.Background(), true, today, 3, start)
		if err != nil {
			t.Fatal(err)
		}
		if len(due) > 3 {
			t.Fatalf("a page of %d rows with limit 3", len(due))
		}
		for _, d := range due {
			seen[d.Recurrence.ID]++
		}
		pages++
		if next == nil {
			break
		}
		start = next
	}
	for id := range ours {
		if seen[id] != 1 {
			t.Fatalf("recurrence %s was seen %d times, want once", id, seen[id])
		}
	}
	if pages < 3 {
		t.Fatalf("%d pages for at least 8 rows with limit 3", pages)
	}
}

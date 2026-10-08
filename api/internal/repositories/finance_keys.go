package repositories

import (
	"fmt"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func BillSK(id string) string       { return "BILL#" + id }
func RecurrenceSK(id string) string { return "RECURRENCE#" + id }

// OccurrenceSK is the materialisation lock row: (recurrence, nominal day).
func OccurrenceSK(recurrenceID string, nominal brcal.Date) string {
	return "OCCURRENCE#" + recurrenceID + "#" + nominal.String()
}

// OpenPK is the open-index partition: the space and the direction. It begins
// with the space key like every finance tenant partition.
func OpenPK(sp space.ResolvedSpace, dir finance.Direction) string {
	return sp.PK() + "#" + string(dir)
}

// OpenSK orders a direction's open bills by due date.
func OpenSK(due brcal.Date, id string) string { return due.String() + "#" + id }

// MaterialisePK and AutoSettlePK are the job indexes' partitions. They are the
// only finance keys that do not begin with a space, by design (ADR 0002): one
// partition per mode and job, ordered by date, so a missed day is caught up by
// the next run instead of needing its own -date.
func MaterialisePK(livemode bool) string { return Mode(livemode) + "#finance-materialize" }
func AutoSettlePK(livemode bool) string  { return Mode(livemode) + "#finance-autosettle" }

// ScheduleSK is {date}#{owner}#{id}. The owner (an organization id or USER#sub)
// lets the job rebuild the row's space with space.ForJob; neither the owner nor
// the id contains a date-shaped prefix, so the first '#' ends the date.
func ScheduleSK(date brcal.Date, owner, id string) string {
	return date.String() + "#" + owner + "#" + id
}

// ParseScheduleSK is the inverse of ScheduleSK. An owner may itself contain
// '#' (USER#sub), so the id is whatever follows the LAST '#'.
func ParseScheduleSK(sk string) (date brcal.Date, owner, id string, err error) {
	first := strings.IndexByte(sk, '#')
	last := strings.LastIndexByte(sk, '#')
	if first < 0 || last <= first {
		return brcal.Date{}, "", "", fmt.Errorf("malformed schedule key %q", sk)
	}
	date, err = brcal.Parse(sk[:first])
	if err != nil {
		return brcal.Date{}, "", "", fmt.Errorf("malformed schedule key %q: %w", sk, err)
	}
	return date, sk[first+1 : last], sk[last+1:], nil
}

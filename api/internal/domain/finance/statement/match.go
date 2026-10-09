package statement

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
)

// Keyed is a line with its idempotency key in its account.
type Keyed struct {
	Line
	Key string
	// Fallback is the content key ("H:" + hash) of a line keyed by its FITID.
	// A bank may reuse a FITID for a later, different transaction; when the
	// FITID's lock was claimed for another date or amount, the line is keyed by
	// its content instead of being dropped as a duplicate.
	Fallback string
}

// Keys gives every line the key its account's lock row is written under, so
// importing the same file twice — or two exports whose periods overlap — adds
// each transaction once (spec § 3.7):
//
//   - a FITID that is unique in the file is the key ("F:" + FITID);
//   - otherwise — no FITID, or a FITID the bank repeated for different lines in
//     one file — the key is a hash of (account, FITID, date, amount,
//     description, ordinal among identical lines). The ordinal keeps two real,
//     identical purchases (two coffees, same day, same price) as two lines, and
//     an overlapping export that holds one of them more than before adds only
//     the new one.
//
// The prefixes keep a FITID from ever colliding with a hash.
func Keys(accountID string, lines []Line) []Keyed {
	fitids := map[string]int{}
	for _, l := range lines {
		if l.FITID != "" {
			fitids[l.FITID]++
		}
	}
	ordinal := map[string]int{}
	out := make([]Keyed, len(lines))
	for i, l := range lines {
		ident := strings.Join([]string{accountID, l.FITID, l.Date.String(),
			strconv.FormatInt(int64(l.Amount), 10), strings.ToUpper(l.Description)}, "\x00")
		ordinal[ident]++
		sum := sha256.Sum256([]byte(ident + "\x00" + strconv.Itoa(ordinal[ident])))
		hashKey := "H:" + hex.EncodeToString(sum[:16])
		if l.FITID != "" && fitids[l.FITID] == 1 {
			out[i] = Keyed{Line: l, Key: "F:" + l.FITID, Fallback: hashKey}
			continue
		}
		out[i] = Keyed{Line: l, Key: hashKey}
	}
	return out
}

// MatchWindow is how far, in days, a line's date may be from a bill's due date
// for the line to be offered as that bill's payment (spec § 3.7).
const MatchWindow = 5

// MaxCandidates bounds what one line offers.
const MaxCandidates = 5

// DirectionOf is the kind of bill a line can settle: money that left pays a
// payable, money that came in receives a receivable.
func DirectionOf(amount billing.Cents) finance.Direction {
	if amount < 0 {
		return finance.Payable
	}
	return finance.Receivable
}

// Candidates returns the open bills a line may settle: in the line's account,
// of the line's direction, for exactly its amount, due within MatchWindow days
// of its date — the closest due date first, then the earlier, then by id. A
// bill may be offered to several lines; only one confirmation can settle it.
func Candidates(accountID string, l Line, bills []finance.Bill) []finance.Bill {
	dir, amount := DirectionOf(l.Amount), abs(l.Amount)
	var out []finance.Bill
	for _, b := range bills {
		if b.Status != finance.BillForecast || b.AccountID != accountID || b.Direction != dir || b.Amount != amount {
			continue
		}
		if distance(l, b) > MatchWindow {
			continue
		}
		out = append(out, b)
	}
	slices.SortStableFunc(out, func(a, b finance.Bill) int {
		if da, db := distance(l, a), distance(l, b); da != db {
			return da - db
		}
		if c := a.Due.Compare(b.Due); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(out) > MaxCandidates {
		out = out[:MaxCandidates]
	}
	return out
}

func distance(l Line, b finance.Bill) int {
	d := l.Date.DaysBetween(b.Due)
	if d < 0 {
		return -d
	}
	return d
}

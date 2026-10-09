package repositories

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func BillSK(id string) string     { return "BILL#" + id }
func CardSK(id string) string     { return "CARD#" + id }
func PurchaseSK(id string) string { return "PURCHASE#" + id }

// ImportSK is an import's row in the space partition; its lines live in the
// import's own partition (ImportPK), so listing imports never reads lines.
func ImportSK(id string) string { return "IMPORT#" + id }
func ImportPK(sp space.ResolvedSpace, importID string) string {
	return sp.PK() + "#IMPORT#" + importID
}

// ImportLineSK orders an import's lines as the file had them.
func ImportLineSK(n int) string { return fmt.Sprintf("LINE#%05d", n) }

// ImportLockSK is the lock that makes one transaction import once into one
// account: key is statement.Keys' ("F:" + FITID, or "H:" + a hash).
func ImportLockSK(accountID, key string) string { return "FITID#" + accountID + "#" + key }

// CSVMappingSK is an account's saved CSV column mapping.
func CSVMappingSK(accountID string) string { return "CSVMAP#" + accountID }

// StatementSK is a closed statement's row; ItemSK sorts a statement's items
// right after it, so one prefix Query on StatementSK(m) returns both.
func StatementSK(m finance.Month) string { return "STATEMENT#" + m.String() }
func ItemSK(m finance.Month, purchaseID, tag string) string {
	return StatementSK(m) + "#ITEM#" + purchaseID + "#" + tag
}

// CardPK is one card's partition: its purchases, statements and items.
func CardPK(sp space.ResolvedSpace, cardID string) string { return sp.PK() + "#CARD#" + cardID }
func RecurrenceSK(id string) string                       { return "RECURRENCE#" + id }

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
func ClosePK(livemode bool) string       { return Mode(livemode) + "#finance-close" }

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

// idempotentID derives a row id from (space, kind, idempotency key): the same
// request, however concurrently it is repeated, names the same row. The space is
// part of the hash so one space's key can never name another space's row.
func idempotentID(sp space.ResolvedSpace, kind, key string) string {
	sum := sha256.Sum256([]byte(sp.PK() + "\x00" + kind + "\x00" + key))
	return strings.ToUpper(hex.EncodeToString(sum[:13])) // 26 characters, like a ULID
}

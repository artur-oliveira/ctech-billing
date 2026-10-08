package repositories

import (
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Finance keys. The partition of every row begins with the space key S
// (space.ResolvedSpace.PK): org#mode, or USER#sub#mode. These builders that need
// a partition take the ResolvedSpace itself, never a string, so a key cannot be
// assembled from a request value.

func LedgerSpaceSK() string               { return "SPACE" }
func LedgerAccountSK(id string) string    { return "ACCOUNT#" + id }
func LedgerTxSK(id string) string         { return "TX#" + id }
func LedgerReversalSK(txID string) string { return "REVERSAL#" + txID }
func LedgerSummarySK(m finance.Month, account string) string {
	return "SUMMARY#" + m.String() + "#" + account
}

// LedgerEntryPK is the partition of one account's entries: S#ACCOUNT#{id}.
func LedgerEntryPK(sp space.ResolvedSpace, account string) string {
	return sp.PK() + "#ACCOUNT#" + account
}

// LedgerEntrySK orders an account's entries by date, then transaction, then leg,
// so a statement for a period is one range Query.
func LedgerEntrySK(date brcal.Date, txID string, leg int) string {
	return fmt.Sprintf("ENTRY#%s#%s#%02d", date, txID, leg)
}

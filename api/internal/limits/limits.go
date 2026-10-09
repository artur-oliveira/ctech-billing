// Package limits holds the bounds on what a person may type, shared by the API's
// request validation and the UI (ui/src/lib/limits.json, kept equal by
// TestTheUIReadsTheseLimits). The API is the authority; the UI enforcing the
// same numbers only spares a round trip.
package limits

import (
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

const (
	// MaxAmountCents is the largest amount anyone types: R$ 9.999.999.999,99,
	// under the ledger's own hard bound (finance.MaxLegAmount, R$ 10 billion).
	MaxAmountCents = 999_999_999_999

	// MaxFutureYears bounds a date in the future (a due date, a recurrence
	// start); MaxRecurrenceYears bounds a recurrence's end after its start.
	MaxFutureYears     = 10
	MaxRecurrenceYears = 50

	// Text lengths, in characters (runes).
	AccountName  = 80
	Description  = 200
	Memo         = 140
	ProductName  = 120
	OwnerKey     = 64
	CustomerName = 200
	LegalName    = 120
	Address      = 240
	Email        = 254
	Reason       = 500
	ExternalRef  = 128
	TaxID        = 18 // "00.000.000/0000-00", the longest formatted document

	MaxInstallments      = 48
	MaxNetDays           = 90
	MaxSubscriptionItems = 20
	MaxUsageQuantity     = 1_000_000_000
)

// MinDate is the earliest civil date accepted anywhere: older is a typo.
var MinDate = brcal.New(2000, 1, 1)

// MaxDate is the latest date a future-facing field accepts, from today.
func MaxDate(today brcal.Date) brcal.Date { return today.AddYears(MaxFutureYears) }

package finance

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidAccount wraps every reason a ledger account is refused.
var ErrInvalidAccount = errors.New("invalid ledger account")

const maxAccountName = 80

// LedgerAccount is an account, a category or a system account (spec § 3.1).
// Users see accounts and categories; the class and the DRE group are what the
// reports read.
type LedgerAccount struct {
	ID    string
	Name  string
	Class AccountClass
	// Group places an income or expense account in the DRE (spec § 3.3). It is
	// empty for asset, liability and equity accounts, which are not in the DRE.
	Group  DREGroup
	System bool
}

// Validate refuses an account the ledger could not report on.
func (a LedgerAccount) Validate() error {
	switch {
	case a.ID == "" || strings.ContainsAny(a.ID, "#/ "):
		return fmt.Errorf("%w: id is required and cannot contain '#', '/' or a space", ErrInvalidAccount)
	case strings.TrimSpace(a.Name) == "" || len(a.Name) > maxAccountName:
		return fmt.Errorf("%w: name is required and at most %d characters", ErrInvalidAccount, maxAccountName)
	case !validClass(a.Class):
		return fmt.Errorf("%w: unknown class %q", ErrInvalidAccount, a.Class)
	case a.Group != "" && !a.Group.AllowsClass(a.Class):
		return fmt.Errorf("%w: group %q does not take class %q", ErrInvalidAccount, a.Group, a.Class)
	}
	return nil
}

func validClass(c AccountClass) bool {
	switch c {
	case ClassAsset, ClassLiability, ClassIncome, ClassExpense, ClassEquity:
		return true
	}
	return false
}

// DefaultSystemAccounts returns the three accounts created with every space and
// the SystemAccounts the posting rules take. Fixed ids: they are never listed to
// or edited by the user, and a space must be able to find them without a lookup.
func DefaultSystemAccounts() (SystemAccounts, []LedgerAccount) {
	sys := SystemAccounts{Payables: "sys-payables", Receivables: "sys-receivables", OpeningBalance: "sys-opening"}
	return sys, []LedgerAccount{
		{ID: sys.Payables, Name: "Contas a pagar", Class: ClassLiability, System: true},
		{ID: sys.Receivables, Name: "Contas a receber", Class: ClassAsset, System: true},
		{ID: sys.OpeningBalance, Name: "Saldos iniciais", Class: ClassEquity, System: true},
	}
}

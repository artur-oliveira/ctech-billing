package repositories

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
)

// A rename that passed its read but lost its condition (an archive landed in
// between) is the person's 422, not a 500; a conflict or a throttle stays what
// it is, so the caller may retry it.
func TestARenameThatLostItsConditionIsInvalidAccount(t *testing.T) {
	for name, err := range map[string]error{
		"account alone":         canceled("ConditionalCheckFailed"),
		"card then its account": canceled("None", "ConditionalCheckFailed"),
	} {
		if got := renameWriteErr(err); !errors.Is(got, finance.ErrInvalidAccount) {
			t.Errorf("%s: %v, want ErrInvalidAccount", name, got)
		}
	}
	conflict := canceled("None", "TransactionConflict")
	if got := renameWriteErr(conflict); errors.Is(got, finance.ErrInvalidAccount) || got == nil {
		t.Errorf("a conflict became %v; it must pass through", got)
	}
	if renameWriteErr(nil) != nil {
		t.Error("no error became one")
	}
}

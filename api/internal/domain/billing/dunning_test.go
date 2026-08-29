package billing

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// A schedule is validated before it is stored because every rule here describes
// a way of hurting a real customer, and none of them is visible in the data
// afterwards — a policy that escalates on day −3 looks perfectly ordinary in a
// table.

func TestScheduleRejectsTheWaysItCanHurtSomebody(t *testing.T) {
	cases := map[string]DunningSchedule{
		"unordered offsets perform steps in a sequence nobody wrote": {
			{Offset: 3, Action: DunningRemind},
			{Offset: 1, Action: DunningRemind},
		},
		"two steps on the same day are one of them silently lost": {
			{Offset: 1, Action: DunningRemind},
			{Offset: 1, Action: DunningEscalate},
		},
		"giving up before the end leaves steps after there is nothing left": {
			{Offset: 1, Action: DunningAbandon},
			{Offset: 10, Action: DunningRemind},
		},
		"escalating before the due date restricts service over a bill that is not late": {
			{Offset: -3, Action: DunningEscalate},
		},
		"an unknown action is a step the job cannot perform": {
			{Offset: 1, Action: "shout"},
		},
	}

	for name, policy := range cases {
		if err := policy.Validate(); err == nil {
			t.Errorf("accepted a policy where %s", name)
		}
	}
}

func TestEmptyScheduleIsValidAndMeansInherit(t *testing.T) {
	if err := DunningSchedule(nil).Validate(); err != nil {
		t.Fatalf("an empty policy must be valid — it means inherit: %v", err)
	}
	if err := DefaultDunningPolicy.Validate(); err != nil {
		t.Fatalf("the built-in policy must pass its own rules: %v", err)
	}
}

// The resolution order is the whole feature: most specific answer somebody
// actually wrote.
func TestResolvePrefersTheProductThenTheOrganization(t *testing.T) {
	product := DunningSchedule{{Offset: 5, Action: DunningRemind}}
	org := DunningSchedule{{Offset: 2, Action: DunningRemind}}

	if got := ResolveDunningPolicy(org, []DunningSchedule{product}); got[0].Offset != 5 {
		t.Errorf("product policy = day %d, want the product's 5", got[0].Offset)
	}
	if got := ResolveDunningPolicy(org, []DunningSchedule{nil}); got[0].Offset != 2 {
		t.Errorf("fallback = day %d, want the organization's 2", got[0].Offset)
	}
	if got := ResolveDunningPolicy(nil, nil); len(got) != len(DefaultDunningPolicy) {
		t.Errorf("last fallback has %d steps, want the built-in %d", len(got), len(DefaultDunningPolicy))
	}
}

// A subscription billing two products with different schedules has no
// defensible "the" policy. Picking the first item's would make the answer
// depend on the order somebody added them.
func TestDisagreeingProductsFallBackToTheOrganization(t *testing.T) {
	a := DunningSchedule{{Offset: 5, Action: DunningRemind}}
	b := DunningSchedule{{Offset: 9, Action: DunningRemind}}
	org := DunningSchedule{{Offset: 2, Action: DunningRemind}}

	got := ResolveDunningPolicy(org, []DunningSchedule{a, b})
	if len(got) != 1 || got[0].Offset != 2 {
		t.Fatalf("policy = %+v, want the organization's", got)
	}
}

// Two products that agree are not a disagreement — the common case of one plan
// split across several products must not silently lose its schedule.
func TestAgreeingProductsKeepTheirPolicy(t *testing.T) {
	same := DunningSchedule{{Offset: 5, Action: DunningRemind}}
	got := ResolveDunningPolicy(nil, []DunningSchedule{same, same.Clone()})
	if len(got) != 1 || got[0].Offset != 5 {
		t.Fatalf("policy = %+v, want the product's", got)
	}
}

// The resolved policy must not alias the row it came from: an invoice carries
// its own copy, and a shared slice is a policy edit that rewrites history.
func TestResolveReturnsACopy(t *testing.T) {
	product := DunningSchedule{{Offset: 5, Action: DunningRemind}}
	got := ResolveDunningPolicy(nil, []DunningSchedule{product})
	got[0].Offset = 99
	if product[0].Offset != 5 {
		t.Fatal("resolving aliased the product's own policy")
	}
}

// An invoice issued before per-plan policies existed carries none, and must
// still be chased on the built-in schedule rather than skipped forever.
func TestInvoiceWithNoStoredPolicyFollowsTheDefault(t *testing.T) {
	inv := &Invoice{}
	if len(inv.Schedule()) != len(DefaultDunningPolicy) {
		t.Fatalf("schedule has %d steps, want the built-in %d", len(inv.Schedule()), len(DefaultDunningPolicy))
	}
	due := brcal.New(2026, time.March, 10)
	first := inv.Schedule().FirstDunningDate(due)
	if want := due.AddDays(DefaultDunningPolicy[0].Offset); first != want {
		t.Fatalf("first dunning date = %s, want %s", first, want)
	}
}

func TestScheduleDatesAndActionsAreIndexedByStep(t *testing.T) {
	policy := DunningSchedule{
		{Offset: -3, Action: DunningRemind},
		{Offset: 10, Action: DunningEscalate},
	}
	due := brcal.New(2026, time.March, 10)

	if d, ok := policy.DunningDate(due, 1); !ok || d != due.AddDays(10) {
		t.Errorf("step 1 falls on %s, want %s", d, due.AddDays(10))
	}
	if _, ok := policy.DunningDate(due, 2); ok {
		t.Error("a step past the end of the policy has no date")
	}
	if action, _ := policy.ActionAt(1); action != DunningEscalate {
		t.Errorf("step 1 action = %q", action)
	}
	if policy.IsOverdueStep(0) {
		t.Error("a step before the due date is not overdue")
	}
	if !policy.IsOverdueStep(1) {
		t.Error("a step after the due date is overdue")
	}
}

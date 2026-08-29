package billing

import (
	"fmt"
	"slices"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Dunning: what happens to an invoice nobody pays.
//
// **This is not charge retry, and the difference is the whole design.** On a
// card rail, dunning means trying the charge again — the money moves on the
// merchant's initiative, so a failure is worth repeating. PIX is a pull: the
// customer pays, billing cannot debit anybody. There is nothing to retry.
//
// So dunning here is three things, none of which is a second charge attempt:
// remind a person, gate the service they are not paying for, and eventually stop
// pretending the invoice will be collected. The reminders are the only part that
// changes whether the money arrives; the rest is bookkeeping honest enough that
// an unpaid invoice does not sit OPEN forever looking like revenue.

// DunningAction is what a step does when its day arrives.
type DunningAction string

const (
	// DunningRemind emails the customer with the payment link.
	DunningRemind DunningAction = "remind"
	// DunningEscalate moves the subscription to PAST_DUE, which is the signal a
	// consuming product acts on to restrict access.
	DunningEscalate DunningAction = "escalate"
	// DunningAbandon marks the invoice UNCOLLECTIBLE and cancels the
	// subscription. It is the end of the policy, not a failure of it.
	DunningAbandon DunningAction = "abandon"
)

// DunningStep is one scheduled action, expressed in days from the due date.
type DunningStep struct {
	// Offset is days relative to the due date. Negative is before it.
	Offset int
	Action DunningAction
}

// DefaultDunningPolicy is the schedule an invoice follows when nothing
// overrides it — which is most of them, and deliberately: a merchant who has not
// thought about dunning gets one that works rather than none.
//
// A product may carry its own (Product.DunningPolicy) and an organization may
// change its default (Organization.DunningPolicy). What an invoice actually
// follows is **copied onto it when it is finalized**, never looked up while it
// is being chased — see Invoice.Policy. Editing a policy therefore changes what
// happens to invoices issued afterwards and nothing about the ones already in
// flight, which is the only version of this feature that is safe: the invoice
// stores the step it has reached, and a step index means nothing if the schedule
// under it can be rewritten.
//
// The offsets:
//
//   - **−3**: a courtesy note before the invoice is even late. It is the only
//     step that prevents a problem rather than reacting to one, and for a PIX
//     bill "you have three days" is the message most likely to be acted on.
//   - **+1**: the invoice is late. Not on the due date itself — a payment made
//     that afternoon is not late, and an email saying so is one that teaches
//     people to ignore the next.
//   - **+3, +7**: the reminders that carry the escalation warning.
//   - **+10**: PAST_DUE. Access is restricted, and the consuming product hears
//     `subscription.past_due` and acts on it.
//   - **+30**: give up. UNCOLLECTIBLE, subscription canceled. A month is long
//     enough that nobody can say they were not told, and short enough that the
//     books are not carrying a receivable that will never arrive.
var DefaultDunningPolicy = DunningSchedule{
	{Offset: -3, Action: DunningRemind},
	{Offset: 1, Action: DunningRemind},
	{Offset: 3, Action: DunningRemind},
	{Offset: 7, Action: DunningRemind},
	{Offset: 10, Action: DunningEscalate},
	{Offset: 30, Action: DunningAbandon},
}

// DunningSchedule is an ordered policy. It is a named type rather than a bare
// slice so the rules that make a schedule valid live with it, and so an empty
// one is a meaningful value: "use the default".
type DunningSchedule []DunningStep

// ErrInvalidDunningPolicy reports a schedule that cannot be followed.
var ErrInvalidDunningPolicy = fmt.Errorf("invalid dunning policy")

// maxDunningSteps bounds a stored policy. It is a guard against a schedule
// pasted in by mistake, not a product limit: eight reminders is already more
// mail than anybody reads, and the row is copied onto every invoice.
const maxDunningSteps = 12

// Validate reports whether this schedule can be followed.
//
// Three rules, each of which prevents a specific way of hurting a customer:
// ordered offsets (a policy that goes backwards performs steps in a sequence
// nobody wrote), at most one abandon and it last (there is nothing after giving
// up), and no escalation before the due date (restricting service over a bill
// that is not yet late).
func (p DunningSchedule) Validate() error {
	if len(p) == 0 {
		return nil // empty means "inherit", and inheriting is always valid
	}
	if len(p) > maxDunningSteps {
		return fmt.Errorf("%w: %d steps is more than %d", ErrInvalidDunningPolicy, len(p), maxDunningSteps)
	}
	for i, step := range p {
		switch step.Action {
		case DunningRemind, DunningEscalate, DunningAbandon:
		default:
			return fmt.Errorf("%w: unknown action %q", ErrInvalidDunningPolicy, step.Action)
		}
		if i > 0 && step.Offset <= p[i-1].Offset {
			return fmt.Errorf("%w: step %d is at day %d, which is not after day %d",
				ErrInvalidDunningPolicy, i, step.Offset, p[i-1].Offset)
		}
		if step.Action == DunningAbandon && i != len(p)-1 {
			return fmt.Errorf("%w: giving up is the last step, not step %d", ErrInvalidDunningPolicy, i)
		}
		if step.Action != DunningRemind && step.Offset < 0 {
			return fmt.Errorf("%w: %s at day %d would act before the invoice is even late",
				ErrInvalidDunningPolicy, step.Action, step.Offset)
		}
	}
	return nil
}

// Clone copies the schedule. Policies are copied onto invoices and must never
// be shared with the row they came from — the same rule metadata follows
// (ADR 0008), and for the same reason.
func (p DunningSchedule) Clone() DunningSchedule {
	if len(p) == 0 {
		return nil
	}
	return slices.Clone(p)
}

// ResolveDunningPolicy decides which schedule an invoice is issued under.
//
// The order is product, then organization, then the built-in default: the most
// specific answer somebody actually wrote. Products whose policies **disagree**
// fall back to the organization's, rather than picking one of them — a
// subscription that bills two plans with different schedules has no defensible
// "the" policy, and silently choosing the first item's would make the answer
// depend on the order somebody added them.
func ResolveDunningPolicy(org DunningSchedule, products []DunningSchedule) DunningSchedule {
	var chosen DunningSchedule
	for _, p := range products {
		if len(p) == 0 {
			continue
		}
		if chosen == nil {
			chosen = p
			continue
		}
		if !slices.Equal(chosen, p) {
			chosen = nil
			break
		}
	}
	if len(chosen) > 0 {
		return chosen.Clone()
	}
	if len(org) > 0 {
		return org.Clone()
	}
	return DefaultDunningPolicy.Clone()
}

// DunningDate returns the day step n falls on for an invoice due on dueDate.
func (p DunningSchedule) DunningDate(dueDate brcal.Date, step int) (brcal.Date, bool) {
	if step < 0 || step >= len(p) {
		return brcal.Date{}, false
	}
	return dueDate.AddDays(p[step].Offset), true
}

// ActionAt returns what step n does.
func (p DunningSchedule) ActionAt(step int) (DunningAction, bool) {
	if step < 0 || step >= len(p) {
		return "", false
	}
	return p[step].Action, true
}

// IsOverdueStep reports whether step n falls after the due date, which is what
// decides the tone of the message rather than its content.
func (p DunningSchedule) IsOverdueStep(step int) bool {
	return step >= 0 && step < len(p) && p[step].Offset > 0
}

// FirstDunningDate is when an invoice on this schedule first enters the queue.
func (p DunningSchedule) FirstDunningDate(dueDate brcal.Date) brcal.Date {
	d, _ := p.DunningDate(dueDate, 0)
	return d
}

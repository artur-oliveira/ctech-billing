package billing

import "gopkg.aoctech.app/billing/api/internal/domain/brcal"

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

// DunningPolicy is the schedule every unpaid invoice follows.
//
// One policy, not one per plan. Per-plan dunning is a real feature and this is
// deliberately not it: a configurable schedule needs a place to configure it, a
// migration for existing plans, and a console screen — and none of that changes
// the outcome for the only tenant that exists. When a merchant needs their own,
// this slice becomes a field on the plan and the shape does not change.
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
var DunningPolicy = []DunningStep{
	{Offset: -3, Action: DunningRemind},
	{Offset: 1, Action: DunningRemind},
	{Offset: 3, Action: DunningRemind},
	{Offset: 7, Action: DunningRemind},
	{Offset: 10, Action: DunningEscalate},
	{Offset: 30, Action: DunningAbandon},
}

// DunningDate returns the day step n falls on for an invoice due on dueDate.
func DunningDate(dueDate brcal.Date, step int) (brcal.Date, bool) {
	if step < 0 || step >= len(DunningPolicy) {
		return brcal.Date{}, false
	}
	return dueDate.AddDays(DunningPolicy[step].Offset), true
}

// NextDunningStep returns the step after the one just performed, and whether
// there is one.
//
// The step index is stored on the invoice rather than derived from the date,
// and that is what makes the job re-runnable: running a missed day twice
// performs each step once, because the invoice has already moved past it.
func NextDunningStep(current int) (int, bool) {
	next := current + 1
	return next, next < len(DunningPolicy)
}

// DunningActionAt returns what step n does.
func DunningActionAt(step int) (DunningAction, bool) {
	if step < 0 || step >= len(DunningPolicy) {
		return "", false
	}
	return DunningPolicy[step].Action, true
}

// FirstDunningDate is when an invoice first enters the dunning queue.
func FirstDunningDate(dueDate brcal.Date) brcal.Date {
	d, _ := DunningDate(dueDate, 0)
	return d
}

package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Dunner chases invoices nobody has paid.
//
// It performs **one step per invoice per run**, the step the invoice is standing
// on, and then moves the invoice to the next one. That is what makes a missed
// day recoverable by re-running with -date: the step index lives on the row, so
// replaying a day performs each step exactly once.
//
// It never retries a charge, because there is nothing to retry — PIX is a pull
// (see billing.DunningPolicy). What it does is remind a person, gate a service,
// and eventually stop carrying a receivable that is not going to arrive.
type Dunner struct {
	invoices  *repositories.InvoiceRepository
	subs      *repositories.SubscriptionRepository
	customers *repositories.CustomerRepository
	links     *PayLink
	mail      email.Sender
}

// dunningActor is the audit actor for everything this job does. It matches the
// scheduler's, so an audit reader can tell an automatic escalation from an
// operator's without joining anything.
const dunningActor = "dunning"

func NewDunner(
	invoices *repositories.InvoiceRepository,
	subs *repositories.SubscriptionRepository,
	customers *repositories.CustomerRepository,
	links *PayLink,
	mail email.Sender,
) *Dunner {
	return &Dunner{invoices: invoices, subs: subs, customers: customers, links: links, mail: mail}
}

// DunningResult is what one run did.
type DunningResult struct {
	Examined  int
	Reminded  int
	Escalated int
	Abandoned int
	Skipped   int
	Errors    []string
}

func (r *DunningResult) fail(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// Run performs every dunning step scheduled for a date.
//
// One invoice failing never stops the run. A dunning pass that aborts on the
// first customer with a missing email address is a pass that silently stops
// chasing everybody after them.
func (d *Dunner) Run(ctx context.Context, livemode bool, date brcal.Date, now time.Time) DunningResult {
	var res DunningResult

	page, err := d.invoices.DueForDunning(ctx, livemode, date, 200, nil)
	if err != nil {
		res.fail("reading the dunning queue for %s: %v", date, err)
		return res
	}
	res.Examined = len(page.Items)

	for i := range page.Items {
		inv := &page.Items[i]
		if err := d.step(ctx, inv, &res, now); err != nil {
			res.fail("invoice %s: %v", inv.ID, err)
		}
	}
	return res
}

// step performs the one action this invoice is due for.
func (d *Dunner) step(ctx context.Context, inv *billing.Invoice, res *DunningResult, now time.Time) error {
	// The queue is sparse and an invoice leaves it when it stops being
	// collectable, so a non-OPEN row here is a race — it was paid between the
	// query and now. Skipping is correct and worth counting: chasing somebody who
	// has already paid is the single worst thing this job can do.
	if inv.Status != billing.InvoiceOpen {
		res.Skipped++
		return nil
	}

	action, ok := inv.Schedule().ActionAt(inv.DunningStep)
	if !ok {
		// The policy ran out but the row is still armed. Advancing disarms it.
		res.Skipped++
		return d.invoices.AdvanceDunning(ctx, inv, inv.DunningStep, false, now)
	}

	reminded := false
	switch action {
	case billing.DunningRemind:
		if err := d.remind(ctx, inv, now); err != nil {
			// A reminder that could not be sent must not consume the step: the
			// customer was not told, and advancing would skip the only message
			// that changes whether this invoice gets paid.
			return err
		}
		reminded = true
		res.Reminded++

	case billing.DunningEscalate:
		if err := d.escalate(ctx, inv, billing.SubscriptionPastDue, now); err != nil {
			return err
		}
		res.Escalated++

	case billing.DunningAbandon:
		if err := d.abandon(ctx, inv, now); err != nil {
			return err
		}
		res.Abandoned++
	}

	if err := d.invoices.AdvanceDunning(ctx, inv, inv.DunningStep, reminded, now); err != nil {
		if errors.Is(err, repositories.ErrConcurrentModification) {
			// Another instance did this step. Not an error: the work is done.
			return nil
		}
		return err
	}
	return nil
}

func (d *Dunner) remind(ctx context.Context, inv *billing.Invoice, now time.Time) error {
	customer, err := d.customers.Get(ctx, inv.OrganizationID, inv.Livemode, inv.CustomerID)
	if err != nil {
		return fmt.Errorf("reading the customer to remind: %w", err)
	}
	if customer.Email == "" || customer.Anonymized {
		// Nothing to send and nothing to fix by retrying. The step is allowed to
		// advance so the invoice still reaches escalation on schedule.
		slog.Info("no reminder sent", "invoice", inv.ID, "reason", "customer has no address")
		return nil
	}

	url := d.links.URL(inv.OrganizationID, inv.Livemode, inv.ID)
	if url == "" {
		// No CHECKOUT_LINK_SECRET means no payable link, and a reminder with no
		// way to pay is a message that wastes the one chance to be read.
		return fmt.Errorf("payment links are not configured")
	}

	overdue := inv.Schedule().IsOverdueStep(inv.DunningStep)
	due := "Vence em " + inv.DueDate.String()
	if overdue {
		due = "Venceu em " + inv.DueDate.String()
	}

	return d.mail.SendInvoiceReminder(ctx, email.Reminder{
		To:   customer.Email,
		Name: customer.Name,
		// Formatted once, here, from the same helper the portal renders with.
		AmountLabel: inv.AmountDue().String(),
		DueLabel:    due,
		PayURL:      url,
		Overdue:     overdue,
	})
}

// escalate moves the subscription, not the invoice.
//
// The invoice stays OPEN and payable throughout: restricting access is a
// statement about the service, and taking away the ability to pay the bill that
// would restore it is self-defeating.
func (d *Dunner) escalate(ctx context.Context, inv *billing.Invoice, to billing.SubscriptionStatus, now time.Time) error {
	if inv.SubscriptionID == "" {
		return nil // a one-off invoice has no service to gate
	}
	sub, err := d.subs.Get(ctx, inv.OrganizationID, inv.Livemode, inv.SubscriptionID)
	if err != nil {
		return fmt.Errorf("reading the subscription to escalate: %w", err)
	}
	if sub.Status == to {
		return nil
	}
	// The cause depends on where the subscription is going, because the domain
	// accepts different ones per edge: ACTIVE → PAST_DUE is a payment that did
	// not arrive, PAST_DUE → CANCELED is the policy running out.
	cause := billing.CausePaymentFailed
	if to == billing.SubscriptionCanceled {
		cause = billing.CauseDunningExhausted
	}

	// And on where it is coming *from*, for the one case where the same policy
	// step means something different: a subscription that never activated.
	//
	// Its first invoice is real and owed, so the reminders are exactly right —
	// they are the messages most likely to get it paid. The escalation steps are
	// not. Restricting a service is a statement about something the customer had,
	// and an INCOMPLETE subscriber never had it, so there is nothing to take away
	// at D+10 and that step does nothing at all. The end of the policy still ends
	// the subscription, but under the reason that is true — activation never
	// happened — rather than "dunning gave up on a subscriber", which they never
	// became.
	//
	// This is also why there is no separate activation-expiry sweep. One policy
	// covers the whole life of an unpaid first invoice, and it covers it with
	// reminders a second job would not have sent.
	if sub.Status == billing.SubscriptionIncomplete {
		if to != billing.SubscriptionCanceled {
			return nil
		}
		cause = billing.CauseActivationExpired
	}

	if _, err := d.subs.Transition(ctx, sub, to, cause, dunningActor, "", now); err != nil {
		// A subscription the domain refuses to move — already canceled, say — is
		// not this job's problem to force.
		if errors.Is(err, billing.ErrInvalidTransition) {
			slog.Info("no escalation", "subscription", sub.ID, "status", sub.Status, "wanted", to)
			return nil
		}
		return err
	}
	return nil
}

// abandon is the end of the policy: the invoice stops being counted as
// collectable and the service ends.
//
// UNCOLLECTIBLE is not "forgiven". The invoice still exists, still shows what
// was owed, and can still be paid — the state machine allows UNCOLLECTIBLE to
// reach PAID through a reconciled payment precisely so that a customer who pays
// two months late is recorded correctly rather than refused.
func (d *Dunner) abandon(ctx context.Context, inv *billing.Invoice, now time.Time) error {
	if err := d.escalate(ctx, inv, billing.SubscriptionCanceled, now); err != nil {
		return err
	}
	if _, err := d.invoices.Transition(ctx, inv, billing.InvoiceUncollectible, billing.CauseDunningExhausted, dunningActor, "", now); err != nil {
		return fmt.Errorf("marking uncollectible: %w", err)
	}
	return nil
}

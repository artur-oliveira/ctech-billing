package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/settlement"
	"gopkg.aoctech.app/billing/api/internal/wallet"
)

// Errors the collection path returns. Each is a different answer to "why can I
// not pay this?", and collapsing them into one would make the checkout page say
// "erro" to a customer holding a bill.
var (
	// ErrInvoiceNotPayable covers a draft (not a bill yet), a paid one (nothing
	// owed) and a void one (withdrawn).
	ErrInvoiceNotPayable = errors.New("invoice is not payable")
	// ErrNoPayerAccount reports a customer with no ctech-account subject.
	//
	// This is the second blocker on a third-party merchant's checkout, and it is
	// structural rather than missing code: wallet's purchase path is keyed on
	// user_id from the reservation through to the refund, so a payer with no CTech
	// account has nowhere to be recorded. Inventing an id would file a stranger's
	// purchase in somebody's wallet history, so billing refuses here instead —
	// before the call, where the reason is still legible.
	ErrNoPayerAccount = errors.New("customer has no CTech account to charge")
	// ErrAmountMismatch reports a settled charge whose amount is not the amount
	// billing opened it for. It is an alarm, never a partial payment.
	ErrAmountMismatch = errors.New("charge amount does not match the attempt")
	// ErrTestModeNotPayable refuses collection on a test-mode invoice.
	//
	// There is one set of wallet credentials (config.WALLET_CLIENT_*), and wallet
	// has no test rail for this charge kind: OpenCharge goes straight to
	// pix.CreateCharge whatever mode billing thinks it is in. So a test-mode
	// invoice taken through this path opens a **real** PIX charge for real money
	// — and then the settlement never arrives, because the notify-back route
	// resolves the attempt in live mode only. Real money in, test invoice stays
	// open, nothing reconciles the two.
	//
	// Refusing here is not a limitation being papered over, it is the limitation
	// stated at the only place that can act on it. It becomes unnecessary the day
	// wallet has a sandbox charge rail and billing has a second set of
	// credentials to reach it with; until then, silence would be the bug.
	ErrTestModeNotPayable = errors.New("test-mode invoices cannot be collected")
)

// ChargeOpener is the slice of ctech-wallet that collection uses.
//
// It exists so the whole payment path is testable without a wallet. The route
// behind it now ships (docs/specs/2026-08-15-wallet-invoice-charge.md, marked
// Implemented), so the fake in tests/integration is no longer a stand-in for
// something absent — it is the contract test, and it fails the day either side
// drifts from the spec.
type ChargeOpener interface {
	OpenCharge(ctx context.Context, in wallet.OpenChargeInput) (*wallet.Charge, error)
	GetCharge(ctx context.Context, chargeID string) (*wallet.Charge, error)
	VerifySignature(body []byte, header string) bool
}

// Collector turns an open invoice into something a person can pay, and turns
// wallet's confirmation back into a paid invoice.
type Collector struct {
	invoices  *repositories.InvoiceRepository
	payments  *repositories.PaymentRepository
	customers *repositories.CustomerRepository
	orgs      *repositories.OrganizationRepository
	subs      *repositories.SubscriptionRepository
	charges   ChargeOpener
	// bus announces a settlement to whichever instance is holding the payer's
	// screen. Optional: without Valkey the screen falls back to re-reading the
	// invoice, which is slower and equally correct.
	bus settlement.Bus
}

// WithSettlementBus attaches the notification channel.
//
// A setter rather than a constructor argument because it is genuinely optional
// and every existing caller — including four test files — would otherwise pass
// nil to say so.
func (c *Collector) WithSettlementBus(bus settlement.Bus) *Collector {
	c.bus = bus
	return c
}

// NewCollector wires collection.
//
// `subs` is a required argument and not an optional setter like the settlement
// bus, deliberately: a nil bus degrades a screen, while a nil subscription
// repository would silently stop paid invoices from restoring service — which is
// the exact failure this dependency exists to prevent. A missing one should be a
// compile error, not a quiet skip.
func NewCollector(
	invoices *repositories.InvoiceRepository,
	payments *repositories.PaymentRepository,
	customers *repositories.CustomerRepository,
	orgs *repositories.OrganizationRepository,
	subs *repositories.SubscriptionRepository,
	charges ChargeOpener,
) *Collector {
	return &Collector{
		invoices:  invoices,
		payments:  payments,
		customers: customers,
		orgs:      orgs,
		subs:      subs,
		charges:   charges,
	}
}

// VerifyWebhook authenticates a notify-back. It proves the sender, and nothing
// else: what the body claims is never acted on without Confirm re-reading the
// charge.
func (c *Collector) VerifyWebhook(body []byte, signature string) bool {
	return c.charges.VerifySignature(body, signature)
}

// Pay opens a checkout session for an invoice, or returns the one that is still
// usable.
//
// Reusing a live session is not an optimization — it is the correct answer. A
// customer who reloads the page, or clicks "pagar" twice, or comes back from
// their bank app, must see the *same* QR code. Opening a second charge each time
// would leave a trail of live PIX charges against one invoice, any of which could
// be paid, and the second one paid is a duplicate the customer has to ask for
// back.
func (c *Collector) Pay(
	ctx context.Context,
	organizationID string,
	livemode bool,
	invoiceID string,
	actor, requestID string,
	now time.Time,
) (*billing.CheckoutSession, *billing.Invoice, error) {
	// Before the read, because it needs nothing from it and because this is the
	// choke point both callers pass through — the public link and the portal. A
	// guard in each of them is a guard the third caller forgets.
	if !livemode {
		return nil, nil, fmt.Errorf("%w: invoice %s", ErrTestModeNotPayable, invoiceID)
	}
	inv, err := c.invoices.Get(ctx, organizationID, livemode, invoiceID)
	if err != nil {
		return nil, nil, err
	}
	if inv.Status != billing.InvoiceOpen {
		return nil, inv, fmt.Errorf("%w: invoice %s is %s", ErrInvoiceNotPayable, inv.ID, inv.Status)
	}

	session, err := c.payments.LatestSession(ctx, organizationID, livemode, invoiceID)
	if err != nil {
		return nil, nil, err
	}
	if session != nil && session.IsUsable(now) {
		return session, inv, nil
	}

	// The payout gate is checked here and only here on this path (ADR 0005).
	// Tenant zero is enabled because the money lands in CTech's own account; an
	// external merchant stays blocked until the legal opinion and the KYC test
	// clear, and no button anywhere can change that.
	org, err := c.orgs.Get(ctx, organizationID, livemode)
	if err != nil {
		return nil, nil, err
	}
	if err := org.AuthorizeCharge(); err != nil {
		return nil, inv, err
	}

	customer, err := c.customers.Get(ctx, organizationID, livemode, inv.CustomerID)
	if err != nil {
		return nil, nil, err
	}
	if customer.UserID == "" {
		return nil, inv, fmt.Errorf("%w: customer %s", ErrNoPayerAccount, customer.ID)
	}

	attempts, err := c.payments.ListAttempts(ctx, organizationID, livemode, invoiceID)
	if err != nil {
		return nil, nil, err
	}
	attempt := &billing.PaymentAttempt{
		ID:             id.NewWithPrefix(id.PrefixPaymentAttempt),
		OrganizationID: organizationID,
		Livemode:       livemode,
		InvoiceID:      inv.ID,
		AttemptNumber:  len(attempts) + 1,
		Status:         billing.AttemptPending,
		Method:         billing.MethodPIX,
		Amount:         inv.AmountDue(),
	}

	charge, err := c.charges.OpenCharge(ctx, wallet.OpenChargeInput{
		UserID:         customer.UserID,
		Amount:         int64(attempt.Amount),
		Reference:      inv.ID,
		IdempotencyKey: attempt.IdempotencyKey(),
		PayerTaxID:     customer.TaxID,
	})
	if err != nil {
		return nil, inv, err
	}
	attempt.WalletChargeID = charge.ID

	if err := c.payments.CreateAttempt(ctx, attempt, now); err != nil {
		if errors.Is(err, repositories.ErrAttemptExists) {
			// Two clicks arrived together and the other one won. Its session is the
			// one to show — and its charge is this same charge, because the
			// idempotency key both computed is identical.
			session, readErr := c.payments.LatestSession(ctx, organizationID, livemode, invoiceID)
			if readErr == nil && session != nil && session.IsUsable(now) {
				return session, inv, nil
			}
		}
		return nil, inv, err
	}

	session = &billing.CheckoutSession{
		ID:               id.NewWithPrefix(id.PrefixCheckoutSession),
		OrganizationID:   organizationID,
		Livemode:         livemode,
		InvoiceID:        inv.ID,
		Status:           billing.CheckoutOpen,
		ExpiresAt:        chargeExpiry(charge, now.Add(billing.DefaultCheckoutTTL)),
		PaymentAttemptID: attempt.ID,
		PixCode:          charge.PixCode,
	}
	if err := c.payments.CreateSession(ctx, session, now); err != nil {
		return nil, inv, err
	}
	slog.InfoContext(ctx, "checkout opened",
		"invoice_id", inv.ID, "attempt", attempt.AttemptNumber,
		"charge_id", charge.ID, "actor", actor, "request_id", requestID)
	return session, inv, nil
}

// Confirm settles an invoice from a wallet charge.
//
// It is called by the webhook and by reconciliation, and it takes a charge id
// rather than a payload for exactly that reason: the only input either of them
// is allowed to contribute is *which charge to go and ask about*. Everything the
// decision rests on is re-read from wallet inside this function.
//
// Every step is idempotent, because both callers will run it more than once: a
// webhook is retried by wallet's sweep, and reconciliation deliberately revisits
// charges the webhook may already have settled.
func (c *Collector) Confirm(ctx context.Context, livemode bool, chargeID string, cause billing.Cause, requestID string, now time.Time) error {
	actor := settledBy(cause)
	attempt, err := c.payments.GetAttemptByCharge(ctx, livemode, chargeID)
	if err != nil {
		return err
	}
	if attempt.Status == billing.AttemptSucceeded {
		return c.settleInvoice(ctx, attempt, cause, actor, requestID, now)
	}
	if attempt.Status != billing.AttemptPending {
		// FAILED or ABANDONED. A late confirmation for one of those is a real
		// finding, not something to quietly apply: reconciliation gave up on a
		// charge that turned out to settle.
		slog.WarnContext(ctx, "wallet confirmed a charge that is no longer pending",
			"charge_id", chargeID, "attempt_status", attempt.Status, "invoice_id", attempt.InvoiceID)
		return nil
	}

	charge, err := c.charges.GetCharge(ctx, chargeID)
	if err != nil {
		return err
	}
	if !charge.Paid() {
		// The wake-up signal was early or wrong. Wallet is the authority and it
		// says no, so nothing happens — and the retry will ask again.
		slog.InfoContext(ctx, "wallet charge not settled yet",
			"charge_id", chargeID, "status", charge.Status)
		return nil
	}
	if charge.Amount != int64(attempt.Amount) {
		// Refuse rather than reconcile the difference. Marking an invoice paid for
		// the wrong amount is worse than leaving it open: the second is visible on
		// a screen someone reads, the first is not.
		slog.ErrorContext(ctx, "charge settled for an unexpected amount",
			"charge_id", chargeID, "expected", int64(attempt.Amount), "settled", charge.Amount)
		return fmt.Errorf("%w: charge %s settled %d, expected %d",
			ErrAmountMismatch, chargeID, charge.Amount, int64(attempt.Amount))
	}

	if _, err := c.payments.TransitionAttempt(
		ctx, attempt, billing.AttemptSucceeded, cause, actor, requestID, now,
	); err != nil {
		return err
	}
	return c.settleInvoice(ctx, attempt, cause, actor, requestID, now)
}

// actorWallet names the webhook in the audit trail. Not "system": during an
// incident, "who marked this paid" has to distinguish the rail from the sweep
// from an operator.
const actorWallet = "service:ctech-wallet"

// settledBy derives the audit actor from the cause, rather than taking both as
// arguments.
//
// The two are the same fact said twice, and a settlement attributed to the
// webhook under the reconciliation cause — or the reverse — is a trail that
// makes an incident harder to read, not easier. Deriving one from the other
// makes the mismatch unwritable.
func settledBy(cause billing.Cause) string {
	if cause == billing.CauseReconciliation {
		return actorReconciler
	}
	return actorWallet
}

// settleInvoice moves the invoice to PAID, restores the service it pays for, and
// completes the session.
//
// Split out because Confirm reaches it from two places — a fresh confirmation
// and a repeated one whose attempt already succeeded. The second happens
// whenever the invoice write failed after the attempt write, and without this
// path the retry would find a succeeded attempt, do nothing, and leave the
// invoice open forever.
//
// Everything after the invoice write runs on **both** paths, and that is the
// point of the shape rather than an early return: each of the three steps below
// is a separate write that can fail on its own, and the retry that follows has
// to be able to finish the ones that did not happen. A repeat that stopped at
// "already PAID" would leave a subscription permanently unactivated by a payment
// that went through.
func (c *Collector) settleInvoice(ctx context.Context, attempt *billing.PaymentAttempt, cause billing.Cause, actor, requestID string, now time.Time) error {
	inv, err := c.invoices.Get(ctx, attempt.OrganizationID, attempt.Livemode, attempt.InvoiceID)
	if err != nil {
		return err
	}
	if inv.Status != billing.InvoicePaid {
		if _, err := c.invoices.Transition(
			ctx, inv, billing.InvoicePaid, cause, actor, requestID, now,
		); err != nil {
			return err
		}
	}
	c.activateSubscription(ctx, inv, cause, actor, requestID, now)
	c.completeSession(ctx, attempt, cause, actor, requestID, now)
	// Told after the write, never before. A screen that celebrates a payment the
	// database has not recorded is a screen that lies when the write then fails.
	if c.bus != nil {
		c.bus.Settled(ctx, inv.ID)
	}
	return nil
}

// activateSubscription is the subscription's half of a payment landing.
//
// Two edges, one call, and the domain already had both: INCOMPLETE -> ACTIVE is
// a first payment arriving and service being granted for the first time;
// PAST_DUE -> ACTIVE is a customer catching up after dunning already restricted
// them (EventSubscriptionRecovered). Nothing in the service walked either of
// them, so a customer who fell past due on D+10 and paid on D+12 stayed
// PAST_DUE forever — the invoice went PAID and the service never came back.
//
// It never fails the settlement, and that asymmetry is deliberate. The money
// arrived and the invoice is PAID; refusing that because the subscription would
// not move would make wallet retry a payment that has fully happened, and there
// is no "unpay" to retry into. A subscription left behind is visible on a screen
// and fixable by hand. A payment recorded nowhere is neither.
func (c *Collector) activateSubscription(ctx context.Context, inv *billing.Invoice, cause billing.Cause, actor, requestID string, now time.Time) {
	if inv.SubscriptionID == "" {
		return // a one-off invoice gates no service
	}
	sub, err := c.subs.Get(ctx, inv.OrganizationID, inv.Livemode, inv.SubscriptionID)
	if err != nil {
		slog.ErrorContext(ctx, "invoice paid but its subscription could not be read",
			"invoice_id", inv.ID, "subscription_id", inv.SubscriptionID, "error", err)
		return
	}
	if sub.Status != billing.SubscriptionIncomplete && sub.Status != billing.SubscriptionPastDue {
		// ACTIVE already — the ordinary renewal, which is most of them — or paused
		// or canceled, neither of which a payment decides on its own. Checked here
		// rather than left to the domain to refuse, because ACTIVE -> ACTIVE is a
		// legal edge under other causes and an ErrInvalidTransition logged on every
		// renewal is a log nobody reads.
		return
	}
	from := sub.Status
	// CauseInvoicePaid, never the cause that settled the charge: the webhook and
	// the reconciler are two ways of learning the same fact, and what moved the
	// subscription is the payment, not which of them noticed it. The actor still
	// names the messenger, which is where that distinction belongs.
	if _, err := c.subs.Transition(
		ctx, sub, billing.SubscriptionActive, billing.CauseInvoicePaid, actor, requestID, now,
	); err != nil {
		slog.ErrorContext(ctx, "invoice paid but its subscription did not activate",
			"invoice_id", inv.ID, "subscription_id", sub.ID,
			"from", from, "settled_by", cause, "error", err)
		return
	}
	slog.InfoContext(ctx, "subscription activated by payment",
		"invoice_id", inv.ID, "subscription_id", sub.ID, "from", from, "actor", actor)
}

// completeSession closes the page's own state. Failures are logged and swallowed
// on purpose: the invoice is already paid, and refusing the webhook over a
// cosmetic row would make wallet retry a settlement that has fully happened.
func (c *Collector) completeSession(ctx context.Context, attempt *billing.PaymentAttempt, cause billing.Cause, actor, requestID string, now time.Time) {
	session, err := c.payments.LatestSession(ctx, attempt.OrganizationID, attempt.Livemode, attempt.InvoiceID)
	if err != nil || session == nil || session.Status != billing.CheckoutOpen {
		return
	}
	if _, err := c.payments.TransitionSession(
		ctx, session, billing.CheckoutCompleted, cause, actor, requestID, now,
	); err != nil {
		slog.WarnContext(ctx, "invoice paid but checkout session not closed",
			"session_id", session.ID, "invoice_id", attempt.InvoiceID, "error", err)
	}
}

// chargeExpiry prefers what wallet says the charge is good for. The rail's
// expiry is the one that matters — a page that stays live past it shows a QR
// code the bank will refuse.
//
// The fallback is the caller's, because the two callers want different ones. A
// checkout session wants a short TTL so the page stops offering a dead QR code;
// reconciliation wants a long one, because there the fallback decides when to
// give up on a charge, and giving up early on a charge somebody paid is the
// expensive mistake.
func chargeExpiry(charge *wallet.Charge, fallback time.Time) time.Time {
	if charge.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, charge.ExpiresAt); err == nil {
			return t
		}
	}
	return fallback
}

package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var (
	// ErrCustomerTaxIDRequired: a due-date charge names its debtor by CPF or
	// CNPJ (spec § 7.2); a customer without one gets no charge.
	ErrCustomerTaxIDRequired = errors.New("byok: the customer has no tax id")
	// ErrNotByokRail is a BYOK operation on a tenant that collects through
	// the wallet.
	ErrNotByokRail = errors.New("byok: the organization does not collect through a bank connection")
	// ErrByokChargeOpen refuses a new attempt while the current charge is
	// still payable.
	ErrByokChargeOpen = errors.New("byok: the invoice's charge is still open")
)

// ChargeCreator is the Invoicer's hook after an invoice is issued (plan scope
// decision 2): the charge is created after Finalize commits, outside its
// transaction, and never fails the invoice.
type ChargeCreator interface {
	Issued(ctx context.Context, inv *billing.Invoice, now time.Time)
}

// NewAttemptDueDays is the due date of a charge opened again from the console
// ("Gerar novo Pix"): today + 3 days.
const NewAttemptDueDays = 3

// ByokCollector collects a BYOK tenant's invoices through its own bank
// connection (spec 2026-10-11 § 7): it creates, updates, cancels and confirms
// the provider's due-date charges through part A's byokrpc.Session. It never
// calls the wallet client (§ 7.6).
type ByokCollector struct {
	invoices  *repositories.InvoiceRepository
	payments  *repositories.PaymentRepository
	customers *repositories.CustomerRepository
	orgs      *repositories.OrganizationRepository
	conns     *repositories.ByokRepository
	gates     *ChargeGates
	sessions  func(*byokconn.Connection) byokrpc.Session
	// settler is a Collector used only for its settlement half (attempt,
	// invoice, subscription, settlement bus, Finanças): built with no wallet.
	settler *Collector
	mail    ByokMailer
}

func NewByokCollector(invoices *repositories.InvoiceRepository, payments *repositories.PaymentRepository,
	customers *repositories.CustomerRepository, orgs *repositories.OrganizationRepository, conns *repositories.ByokRepository,
	gates *ChargeGates, sessions func(*byokconn.Connection) byokrpc.Session, settler *Collector) *ByokCollector {
	return &ByokCollector{invoices: invoices, payments: payments, customers: customers, orgs: orgs, conns: conns,
		gates: gates, sessions: sessions, settler: settler}
}

// WithMail sends the excess-payment notice to the connection's address.
func (c *ByokCollector) WithMail(m ByokMailer) *ByokCollector { c.mail = m; return c }

// connection is the tenant's collecting connection.
func (c *ByokCollector) connection(ctx context.Context, org *billing.Organization) (*byokconn.Connection, error) {
	if org.Rail() != billing.RailBYOK {
		return nil, ErrNotByokRail
	}
	conn, err := c.conns.GetConnection(ctx, org.AccountOrganizationID, org.Livemode, org.PayoutProvider)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, billing.ErrByokNotConnected
	}
	if err != nil {
		return nil, err
	}
	if conn.State == "" {
		return nil, billing.ErrByokNotConnected
	}
	return conn, nil
}

// session is the connection's calls; the shared sandbox's Pix limiter is
// taken once per provider call (spec § 9).
func (c *ByokCollector) session(ctx context.Context, org *billing.Organization) (byokrpc.Session, error) {
	conn, err := c.connection(ctx, org)
	if err != nil {
		return byokrpc.Session{}, err
	}
	return c.sessions(conn), nil
}

func pix(ctx context.Context, s byokrpc.Session) error { return s.Limits.Pix(ctx, s.Conn) }

// Issued is the Invoicer's hook: an issued, open, positive invoice of a BYOK
// tenant gets its charge now. A refusal or failure is logged and leaves the
// invoice issued (spec § 7.2); checkout and the sweep retry.
func (c *ByokCollector) Issued(ctx context.Context, inv *billing.Invoice, now time.Time) {
	if inv.Status != billing.InvoiceOpen || inv.Collectable() <= 0 {
		return
	}
	org, err := c.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil || org.Rail() != billing.RailBYOK {
		return
	}
	if _, err := c.ensure(ctx, org, inv, "service:byok-issue", "", now); err != nil {
		slog.WarnContext(ctx, "byok charge not created at finalization", "invoice_id", inv.ID, "reason", err.Error())
	}
}

// Ensure creates or completes the invoice's pending BYOK attempt (checkout,
// the sweep, the console).
func (c *ByokCollector) Ensure(ctx context.Context, inv *billing.Invoice, actor, requestID string, now time.Time) (*billing.PaymentAttempt, error) {
	org, err := c.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil {
		return nil, err
	}
	return c.ensure(ctx, org, inv, actor, requestID, now)
}

func (c *ByokCollector) ensure(ctx context.Context, org *billing.Organization, inv *billing.Invoice, actor, requestID string, now time.Time) (*billing.PaymentAttempt, error) {
	latest, n, err := c.latest(ctx, inv)
	if err != nil {
		return nil, err
	}
	switch {
	case latest != nil && latest.Method == billing.MethodBYOK && latest.Status == billing.AttemptPending:
		if latest.Payable != "" {
			return latest, nil
		}
		// Written, never completed: the provider call failed or the process
		// died after it. The same txid makes the retry idempotent.
		return latest, c.createCharge(ctx, org, inv, latest, now)
	case latest != nil:
		// FAILED (expired, removed) or already settled: a new charge is the
		// console's explicit act (NewAttempt), never a side effect of a read.
		return nil, fmt.Errorf("%w: invoice %s has no open charge", ErrInvoiceNotPayable, inv.ID)
	}
	due := inv.DueDate
	if today := brcal.FromTime(now); due.Before(today) {
		due = today
	}
	return c.open(ctx, org, inv, n+1, due, actor, requestID, now)
}

// NewAttempt opens a new charge after the previous one ended unpaid ("Gerar
// novo Pix", spec § 7.3): due today + 3 days.
func (c *ByokCollector) NewAttempt(ctx context.Context, inv *billing.Invoice, actor, requestID string, now time.Time) (*billing.PaymentAttempt, error) {
	org, err := c.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil {
		return nil, err
	}
	latest, n, err := c.latest(ctx, inv)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return c.ensure(ctx, org, inv, actor, requestID, now)
	}
	switch latest.Status {
	case billing.AttemptPending:
		if latest.Method == billing.MethodBYOK && latest.Payable == "" {
			return latest, c.createCharge(ctx, org, inv, latest, now)
		}
		return nil, ErrByokChargeOpen
	case billing.AttemptSucceeded:
		return nil, fmt.Errorf("%w: invoice %s is paid", ErrInvoiceNotPayable, inv.ID)
	}
	return c.open(ctx, org, inv, n+1, brcal.FromTime(now).AddDays(NewAttemptDueDays), actor, requestID, now)
}

func (c *ByokCollector) latest(ctx context.Context, inv *billing.Invoice) (*billing.PaymentAttempt, int, error) {
	if inv.Status != billing.InvoiceOpen || inv.Collectable() <= 0 {
		return nil, 0, fmt.Errorf("%w: invoice %s is %s", ErrInvoiceNotPayable, inv.ID, inv.Status)
	}
	attempts, err := c.payments.ListAttempts(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil || len(attempts) == 0 {
		return nil, 0, err
	}
	last := attempts[len(attempts)-1]
	return &last, len(attempts), nil
}

// open writes a new attempt and its pointer, then creates the charge: the row
// first, because the reference is ours (plan scope decision 2).
func (c *ByokCollector) open(ctx context.Context, org *billing.Organization, inv *billing.Invoice, number int, due brcal.Date, actor, requestID string, now time.Time) (*billing.PaymentAttempt, error) {
	limit, err := c.gates.AuthorizeWithLimit(ctx, org)
	if err != nil {
		return nil, err
	}
	customer, err := c.customers.Get(ctx, inv.OrganizationID, inv.Livemode, inv.CustomerID)
	if err != nil {
		return nil, err
	}
	if !validTaxID(customer.TaxID) {
		return nil, fmt.Errorf("%w: customer %s", ErrCustomerTaxIDRequired, customer.ID)
	}
	a := &billing.PaymentAttempt{
		ID: id.NewWithPrefix(id.PrefixPaymentAttempt), OrganizationID: inv.OrganizationID, Livemode: inv.Livemode,
		InvoiceID: inv.ID, AttemptNumber: number, Status: billing.AttemptPending, Method: billing.MethodBYOK,
		Amount: inv.Collectable(), Provider: org.PayoutProvider, Product: billing.ProductPixDue, DueDate: due, CreatedAt: now.UTC(),
	}
	a.ChargeRef = billing.InterTxid(a.ID)
	if c.immediate(ctx, org, inv) {
		a.ChargeRef = billing.ImmediateInterTxid(a.ID)
	}
	if err := c.payments.CreateByokAttempt(ctx, a, org.AccountOrganizationID, limit, now); err != nil {
		if errors.Is(err, repositories.ErrAttemptExists) || errors.Is(err, repositories.ErrTransactionConflict) {
			// Two presses (or finalization and a checkout) at once: the other
			// one's attempt is this invoice's; complete it if it has to be.
			if winner, _, lerr := c.latest(ctx, inv); lerr == nil && winner != nil && winner.Status == billing.AttemptPending {
				if winner.Payable != "" {
					return winner, nil
				}
				return winner, c.createCharge(ctx, org, inv, winner, now)
			}
		}
		return nil, err
	}
	slog.InfoContext(ctx, "byok attempt opened", "invoice_id", inv.ID, "attempt", a.AttemptNumber, "actor", actor, "request_id", requestID)
	return a, c.createCharge(ctx, org, inv, a, now)
}

// immediate says whether the invoice's Pix is an immediate charge: its service asked for a
// short lifetime and the connection may create them. Without the capability the invoice
// keeps the due-date charge it always had, rather than failing to be payable.
func (c *ByokCollector) immediate(ctx context.Context, org *billing.Organization, inv *billing.Invoice) bool {
	if inv.PixExpiresInSeconds <= 0 {
		return false
	}
	conn, err := c.connection(ctx, org)
	return err == nil && conn.Has(byok.CapabilityImmediateCharge)
}

// capabilityFor is the token a call about this charge needs: an immediate charge's reference
// asks for the immediate-charge scopes, a due-date one's for the collection scopes.
func capabilityFor(ref string) byok.Capability {
	if byok.IsImmediateRef(ref) {
		return byok.CapabilityImmediateCharge
	}
	return byok.CapabilityCollection
}

// createCharge calls the provider for a written attempt (idempotent by its
// reference) and records the artefact.
func (c *ByokCollector) createCharge(ctx context.Context, org *billing.Organization, inv *billing.Invoice, a *billing.PaymentAttempt, now time.Time) error {
	if err := c.gates.Authorize(ctx, org); err != nil {
		return err
	}
	customer, err := c.customers.Get(ctx, inv.OrganizationID, inv.Livemode, inv.CustomerID)
	if err != nil {
		return err
	}
	if !validTaxID(customer.TaxID) {
		return fmt.Errorf("%w: customer %s", ErrCustomerTaxIDRequired, customer.ID)
	}
	sess, err := c.session(ctx, org)
	if err != nil {
		return err
	}
	due := a.DueDate
	if due.IsZero() {
		due = inv.DueDate
	}
	validity := inv.ValidityDays()
	var items []billing.InvoiceItem
	if inv.SubscriptionID != "" && inv.PayerMessage == "" { // the plan's name is on the first line
		items, _ = c.invoices.ListItems(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	}
	in := byok.DueCharge{
		Ref: a.ChargeRef, Due: toByok(due), ValidityDays: validity, Amount: int64(a.Amount),
		FineBasisPoints: inv.Collection.FineBasisPoints, MonthlyInterestBasisPoints: inv.Collection.MonthlyInterestBasisPoints,
		Debtor: byok.Debtor{Name: customer.Name, TaxID: digitsOf(customer.TaxID)}, Message: invoiceChargeDescription(inv, items),
	}
	var expiresAt time.Time
	payableUntil := billing.PayableUntil(due, validity)
	if byok.IsImmediateRef(a.ChargeRef) {
		in.ExpiresInSeconds = inv.PixExpiresInSeconds
		expiresAt = now.Add(time.Duration(inv.PixExpiresInSeconds) * time.Second)
		payableUntil = brcal.FromTime(expiresAt)
	}
	if err := pix(ctx, sess); err != nil {
		return err
	}
	ch, err := byokrpc.Call(ctx, sess, capabilityFor(a.ChargeRef), func(cr byok.Credentials, t byok.Token) (byok.Charge, error) {
		return sess.P.CreateDueCharge(ctx, cr, t, in)
	})
	if err != nil {
		return err
	}
	return c.payments.SetByokCharge(ctx, a, ch.Payable, due, payableUntil, expiresAt, now)
}

// Checkout is Collector.Pay for a BYOK invoice: the attempt's Pix, in a
// checkout session that lives until the charge stops being payable.
func (c *ByokCollector) Checkout(ctx context.Context, org *billing.Organization, inv *billing.Invoice, actor, requestID string, now time.Time) (*billing.CheckoutSession, error) {
	a, err := c.ensure(ctx, org, inv, actor, requestID, now)
	if err != nil {
		return nil, err
	}
	if a.Payable == "" {
		return nil, fmt.Errorf("%w: the charge has no payable artefact yet", byok.ErrUnavailable)
	}
	if !a.PixExpiresAt.IsZero() && !now.Before(a.PixExpiresAt) {
		// The bank will refuse this code: never show it. A new one is the console's explicit act.
		return nil, fmt.Errorf("%w: the Pix of invoice %s has expired", ErrInvoiceNotPayable, inv.ID)
	}
	if s, err := c.payments.LatestSession(ctx, inv.OrganizationID, inv.Livemode, inv.ID); err == nil && s != nil &&
		s.PaymentAttemptID == a.ID && s.IsUsable(now) {
		return s, nil
	}
	s := &billing.CheckoutSession{
		ID: id.NewWithPrefix(id.PrefixCheckoutSession), OrganizationID: inv.OrganizationID, Livemode: inv.Livemode,
		InvoiceID: inv.ID, Status: billing.CheckoutOpen, ExpiresAt: sessionEnd(a),
		PaymentAttemptID: a.ID, PixCode: a.Payable, PayableUntil: a.PayableUntil,
	}
	if err := c.payments.CreateSession(ctx, s, now); err != nil {
		return nil, err
	}
	return s, nil
}

// Voided removes the open charge of an invoice being voided (spec § 7.3).
// paid=true: the charge was concluded meanwhile and the re-query settled the
// invoice, which wins over the void.
func (c *ByokCollector) Voided(ctx context.Context, inv *billing.Invoice, now time.Time) (paid bool, err error) {
	return c.withdraw(ctx, inv, "voided", billing.CausePaymentFailed, now)
}

// withdraw cancels the invoice's pending BYOK charge and fails its attempt.
func (c *ByokCollector) withdraw(ctx context.Context, inv *billing.Invoice, reason string, cause billing.Cause, now time.Time) (bool, error) {
	attempts, err := c.payments.ListAttempts(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil || len(attempts) == 0 {
		return false, err
	}
	a := attempts[len(attempts)-1]
	if a.Method != billing.MethodBYOK || a.Status != billing.AttemptPending {
		return false, nil
	}
	org, err := c.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil {
		return false, err
	}
	paid, err := c.cancelAt(ctx, org, &a, reason, cause, now)
	return paid, err
}

// cancelAt removes one charge at the provider; ErrConcluded hands over to the
// re-query (a paid charge wins), ErrNotFound means it never reached it.
func (c *ByokCollector) cancelAt(ctx context.Context, org *billing.Organization, a *billing.PaymentAttempt, reason string, cause billing.Cause, now time.Time) (bool, error) {
	sess, err := c.session(ctx, org)
	if err != nil {
		return false, err
	}
	if err := pix(ctx, sess); err != nil {
		return false, err
	}
	_, err = byokrpc.Call(ctx, sess, capabilityFor(a.ChargeRef), func(cr byok.Credentials, t byok.Token) (struct{}, error) {
		return struct{}{}, sess.P.CancelCharge(ctx, cr, t, a.ChargeRef)
	})
	switch {
	case errors.Is(err, byok.ErrConcluded):
		out, cerr := c.confirmAttempt(ctx, org, a, WakeManual, now)
		if cerr != nil {
			return false, cerr
		}
		return out == OutcomeSettled || out == OutcomeAlreadySettled, nil
	case err != nil && !errors.Is(err, byok.ErrNotFound):
		return false, err
	}
	a.FailureReason, a.CancelledAt = reason, now.UTC()
	if _, err := c.payments.TransitionAttempt(ctx, a, billing.AttemptFailed, cause, "service:byok", "", now); err != nil &&
		!errors.Is(err, repositories.ErrConcurrentModification) {
		return false, err
	}
	return false, nil
}

// Credited updates the open charge to the invoice's new amount due after a
// credit note (spec § 7.3); a fully credited invoice's charge is removed.
func (c *ByokCollector) Credited(ctx context.Context, inv *billing.Invoice, now time.Time) error {
	if inv.Status != billing.InvoiceOpen {
		return nil
	}
	attempts, err := c.payments.ListAttempts(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil || len(attempts) == 0 {
		return err
	}
	a := attempts[len(attempts)-1]
	if a.Method != billing.MethodBYOK || a.Status != billing.AttemptPending || a.Amount == inv.Collectable() {
		return nil
	}
	org, err := c.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil {
		return err
	}
	if inv.Collectable() <= 0 {
		_, err := c.cancelAt(ctx, org, &a, "credited", billing.CausePaymentFailed, now)
		return err
	}
	sess, err := c.session(ctx, org)
	if err != nil {
		return err
	}
	if err := pix(ctx, sess); err != nil {
		return err
	}
	_, err = byokrpc.Call(ctx, sess, capabilityFor(a.ChargeRef), func(cr byok.Credentials, t byok.Token) (byok.Charge, error) {
		return sess.P.UpdateCharge(ctx, cr, t, a.ChargeRef, int64(inv.Collectable()))
	})
	switch {
	case errors.Is(err, byok.ErrConcluded):
		_, err = c.confirmAttempt(ctx, org, &a, WakeManual, now)
		return err
	case err != nil:
		return err
	}
	return c.payments.SetByokAmount(ctx, &a, inv.Collectable(), now)
}

// SimulatePayment pays a test invoice's charge in the provider's sandbox
// (spec § 9); the confirmation then runs through the real re-query path.
func (c *ByokCollector) SimulatePayment(ctx context.Context, inv *billing.Invoice, now time.Time) error {
	if inv.Livemode {
		return byok.ErrSandboxOnly
	}
	attempts, err := c.payments.ListAttempts(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		return err
	}
	if len(attempts) == 0 || attempts[len(attempts)-1].Method != billing.MethodBYOK || attempts[len(attempts)-1].Status != billing.AttemptPending {
		return fmt.Errorf("%w: invoice %s has no open charge", ErrInvoiceNotPayable, inv.ID)
	}
	a := attempts[len(attempts)-1]
	org, err := c.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil {
		return err
	}
	sess, err := c.session(ctx, org)
	if err != nil {
		return err
	}
	if err := pix(ctx, sess); err != nil {
		return err
	}
	_, err = byokrpc.Call(ctx, sess, capabilityFor(a.ChargeRef), func(cr byok.Credentials, t byok.Token) (struct{}, error) {
		return struct{}{}, sess.P.SimulatePayment(ctx, cr, t, a.ChargeRef)
	})
	return err
}

// validTaxID is an 11-digit CPF or a 14-digit CNPJ once the punctuation goes.
func validTaxID(s string) bool {
	d := digitsOf(s)
	return len(d) == 11 || len(d) == 14
}

func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// excessNotice is the e-mail of an excess payment, to the connection's
// address (owners/admins chose it at connect).
func (c *ByokCollector) excessNotice(ctx context.Context, conn *byokconn.Connection, inv *billing.Invoice, amount billing.Cents) {
	if c.mail == nil || conn.NotifyEmail == "" {
		return
	}
	n := email.ByokNotice{Kind: "excess_payment", Provider: conn.Provider, Amount: int64(amount)}
	if inv != nil {
		n.InvoiceNumber = inv.Number
	}
	if err := c.mail.SendByokNotice(ctx, conn.NotifyEmail, n); err != nil {
		slog.WarnContext(ctx, "byok excess payment notice", "invoice_id", n.InvoiceNumber, "error", err)
	}
}

// sessionEnd is when a checkout page for the attempt stops being offered: an immediate Pix's own
// instant, otherwise the day after the last payable day.
func sessionEnd(a *billing.PaymentAttempt) time.Time {
	if !a.PixExpiresAt.IsZero() {
		return a.PixExpiresAt
	}
	return a.PayableUntil.AddDays(1).Time()
}

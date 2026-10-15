package realtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Change types that exist only on the socket: nobody subscribes a webhook to
// them. The rest of a Change's types are the webhook names (billing.EventType).
const (
	TypePayerRecorded     = "invoice.payer_recorded"
	TypePayoutUpdated     = "payout.updated"
	TypeConnectionUpdated = "connection.updated"
	TypeStatementPosted   = "statement.line_posted"
	TypeBillUpdated       = "bill.updated"
	TypeCustomerUpdated   = "customer.updated"
)

// RealtimeOnlyTypes lists them, for the catalogue test and the docs.
func RealtimeOnlyTypes() []string {
	return []string{TypePayerRecorded, TypePayoutUpdated, TypeConnectionUpdated, TypeStatementPosted, TypeBillUpdated, TypeCustomerUpdated}
}

// Entity names, the same ones the REST resources use.
const (
	EntityInvoice      = "invoice"
	EntityPayment      = "payment"
	EntitySubscription = "subscription"
	EntityCheckout     = "checkout_session"
	EntityCreditNote   = "credit_note"
	EntityCustomer     = "customer"
	EntityPayout       = "payout"
	EntityConnection   = "connection"
	EntityStatement    = "statement"
	EntityBill         = "bill"
)

// CustomerReader is the one read the emitter makes: who the customer's user is,
// for the portal's scope.
type CustomerReader interface {
	Get(ctx context.Context, organizationID string, livemode bool, customerID string) (*billing.Customer, error)
}

const (
	userCacheTTL = 60 * time.Second
	userCacheMax = 5000
)

type cachedUser struct {
	userID  string
	expires time.Time
}

// Emitter builds the Change for an entity's transition and resolves who may hear
// it (spec § 4.1). It implements repositories.ChangeEmitter.
//
// A change carries ids and state words only. The scopes are the only thing it
// derives, from the entity's own organization, mode and customer: nothing here
// comes from a request.
type Emitter struct {
	customers CustomerReader
	now       func() time.Time

	mu    sync.Mutex
	users map[string]cachedUser
}

var _ repositories.ChangeEmitter = (*Emitter)(nil)

func NewEmitter(customers CustomerReader) *Emitter {
	return &Emitter{customers: customers, now: time.Now, users: map[string]cachedUser{}}
}

// WithClock replaces the clock the cache reads. Tests only.
func (e *Emitter) WithClock(now func() time.Time) *Emitter { e.now = now; return e }

// userOf is the customer's ctech-account subject, or "" when the customer has
// none (an API-only customer) or cannot be read. Cached for a minute: a screen
// that refreshes a minute late is fine, a read per transition is not.
func (e *Emitter) userOf(ctx context.Context, org string, live bool, customerID string) string {
	if customerID == "" || e.customers == nil {
		return ""
	}
	key := TenantScope(org, live) + "|" + customerID
	now := e.now()
	e.mu.Lock()
	if c, ok := e.users[key]; ok && now.Before(c.expires) {
		e.mu.Unlock()
		return c.userID
	}
	e.mu.Unlock()

	customer, err := e.customers.Get(ctx, org, live, customerID)
	if err != nil {
		slog.WarnContext(ctx, "realtime: customer unreadable, the portal is not told", "customer_id", customerID, "error", err)
		return ""
	}
	e.mu.Lock()
	if len(e.users) >= userCacheMax {
		clear(e.users) // bounded: a few thousand customers is a wide margin
	}
	e.users[key] = cachedUser{userID: customer.UserID, expires: now.Add(userCacheTTL)}
	e.mu.Unlock()
	return customer.UserID
}

func (e *Emitter) ForInvoice(ctx context.Context, inv *billing.Invoice, typ billing.EventType) *repositories.Change {
	scopes := []string{
		TenantScope(inv.OrganizationID, inv.Livemode),
		InvoiceScope(inv.OrganizationID, inv.Livemode, inv.ID),
	}
	if user := e.userOf(ctx, inv.OrganizationID, inv.Livemode, inv.CustomerID); user != "" {
		scopes = append(scopes, UserScope(user))
	}
	return &repositories.Change{
		Type: string(typ), Entity: EntityInvoice, EntityID: inv.ID, Status: string(inv.Status), Scopes: scopes,
	}
}

// ForAttempt is for the console and the invoice's checkout. A payment attempt
// has no customer on it, so the portal learns of a payment from the invoice.
func (e *Emitter) ForAttempt(_ context.Context, a *billing.PaymentAttempt, typ billing.EventType) *repositories.Change {
	return &repositories.Change{
		Type: string(typ), Entity: EntityPayment, EntityID: a.ID, Status: string(a.Status),
		Scopes: []string{
			TenantScope(a.OrganizationID, a.Livemode),
			InvoiceScope(a.OrganizationID, a.Livemode, a.InvoiceID),
		},
	}
}

func (e *Emitter) ForSubscription(ctx context.Context, sub *billing.Subscription, typ billing.EventType) *repositories.Change {
	scopes := []string{TenantScope(sub.OrganizationID, sub.Livemode)}
	if user := e.userOf(ctx, sub.OrganizationID, sub.Livemode, sub.CustomerID); user != "" {
		scopes = append(scopes, UserScope(user))
	}
	return &repositories.Change{
		Type: string(typ), Entity: EntitySubscription, EntityID: sub.ID, Status: string(sub.Status), Scopes: scopes,
	}
}

func (e *Emitter) ForCheckout(_ context.Context, cs *billing.CheckoutSession, typ billing.EventType) *repositories.Change {
	return &repositories.Change{
		Type: string(typ), Entity: EntityCheckout, EntityID: cs.ID, Status: string(cs.Status),
		Scopes: []string{
			TenantScope(cs.OrganizationID, cs.Livemode),
			InvoiceScope(cs.OrganizationID, cs.Livemode, cs.InvoiceID),
		},
	}
}

func (e *Emitter) ForCreditNote(ctx context.Context, cn *billing.CreditNote, inv *billing.Invoice) *repositories.Change {
	scopes := []string{
		TenantScope(inv.OrganizationID, inv.Livemode),
		InvoiceScope(inv.OrganizationID, inv.Livemode, inv.ID),
	}
	if user := e.userOf(ctx, inv.OrganizationID, inv.Livemode, inv.CustomerID); user != "" {
		scopes = append(scopes, UserScope(user))
	}
	return &repositories.Change{
		Type: string(billing.EventCreditNoteCreated), Entity: EntityCreditNote, EntityID: cn.ID, Scopes: scopes,
	}
}

// ForInvoiceRef is for something that happened to an invoice the caller only
// has the id of (a payer recorded on a payment): the tenant's console and the
// invoice's own checkout hear it.
func (e *Emitter) ForInvoiceRef(org string, live bool, invoiceID, typ, status string) *repositories.Change {
	return &repositories.Change{
		Type: typ, Entity: EntityInvoice, EntityID: invoiceID, Status: status,
		Scopes: []string{TenantScope(org, live), InvoiceScope(org, live, invoiceID)},
	}
}

// ForTenant is for what only the merchant's console shows: a payout, a bank
// connection, a customer.
func (e *Emitter) ForTenant(org string, live bool, entity, entityID, typ, status string) *repositories.Change {
	return &repositories.Change{Type: typ, Entity: entity, EntityID: entityID, Status: status, Scopes: []string{TenantScope(org, live)}}
}

// ForSpace is for Finanças: a statement line posted, a bill settled. The space
// key already carries the mode.
func (e *Emitter) ForSpace(spaceKey, entity, entityID, typ, status string) *repositories.Change {
	return &repositories.Change{Type: typ, Entity: entity, EntityID: entityID, Status: status, Scopes: []string{SpaceScope(spaceKey)}}
}

// Free builds a change that is not a transition: a customer edited, a payer
// recorded, a bank line posted. The caller names the scopes.
func Free(entity, entityID, typ, status string, scopes ...string) *repositories.Change {
	return &repositories.Change{Type: typ, Entity: entity, EntityID: entityID, Status: status, Scopes: scopes}
}

// Entities lists every entity name a Change can carry, for the catalogue test.
func Entities() []string {
	return []string{EntityInvoice, EntityPayment, EntityCheckout, EntityCreditNote, EntitySubscription, EntityCustomer, EntityPayout, EntityConnection, EntityStatement, EntityBill}
}

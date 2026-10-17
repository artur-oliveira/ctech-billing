package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Blocker codes, stable and translated by ctech-account's UI. Never rename.
const (
	// BlockerInvoiceOpen is a live tenant-zero invoice still owed by the person
	// or by a customer of a workspace being erased. The person pays it in the
	// portal (action_url) and asks again.
	BlockerInvoiceOpen = "billing.invoice_open"
	// BlockerInvoiceUncollectible is a live tenant-zero invoice that dunning
	// wrote off with an amount still due. A written-off debt still blocks (user
	// decision 2026-10-08). The portal cannot collect it, so support records the
	// payment (CauseManualPayment), and the blocker has no action_url.
	BlockerInvoiceUncollectible = "billing.invoice_uncollectible"
	// BlockerMerchantTenant is a workspace being erased that has a billing
	// tenant (decision 2026-10-13). Deleting a tenant is its own flow, which does
	// not exist yet, so the person asks support. detail.workspace_ids names them.
	BlockerMerchantTenant = "billing.merchant_tenant"
)

// erasureActor names the saga in the audit trail of what it changes.
const erasureActor = "account-erasure"

// BankConnections is the BYOK surface the purge needs (R23).
type BankConnections interface {
	List(ctx context.Context, sp space.ResolvedSpace) ([]byokconn.Connection, error)
	Disconnect(ctx context.Context, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) error
}

// Eraser is billing's side of the account-deletion saga (ctech-account
// docs/specs/2026-10-06-account-deletion-saga-protocol.md § 4; data inventory
// § 6). Blockers answers the eligibility question, and Purge is the consumer's
// PurgeFunc.
//
// What it keeps and why is ADR 0009 and ADR 0026: the invoice aggregate and the
// canceled subscriptions are documents; tenant-zero customers are anonymized in
// place; the finance spaces of the person and of the workspaces erased with
// them are deleted.
type Eraser struct {
	customers   *repositories.CustomerRepository
	subs        *repositories.SubscriptionRepository
	invoices    *repositories.InvoiceRepository
	orgs        *repositories.OrganizationRepository
	catalog     *repositories.CatalogRepository
	levels      *repositories.LevelRepository
	portalOrg   string
	invoicesURL string

	purger   *repositories.SpacePurger
	spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)
	banks    BankConnections
}

// NewEraser builds the eligibility side. portalOrganizationID is tenant zero
// (empty: there are no customers to look for). invoicesURL is the portal page a
// blocker points the person at (empty: no action_url).
func NewEraser(
	customers *repositories.CustomerRepository,
	subs *repositories.SubscriptionRepository,
	invoices *repositories.InvoiceRepository,
	orgs *repositories.OrganizationRepository,
	catalog *repositories.CatalogRepository,
	levels *repositories.LevelRepository,
	portalOrganizationID, invoicesURL string,
) *Eraser {
	return &Eraser{customers: customers, subs: subs, invoices: invoices, orgs: orgs, catalog: catalog, levels: levels,
		portalOrg: portalOrganizationID, invoicesURL: invoicesURL}
}

// WithPurge adds what Purge needs. spaceFor is space.ForJob, passed in from a
// cmd/ binary: ForJob is forbidden under internal/.
func (e *Eraser) WithPurge(p *repositories.SpacePurger, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error), banks BankConnections) *Eraser {
	e.purger, e.spaceFor, e.banks = p, spaceFor, banks
	return e
}

// debt is what a customer still owes CTech, live mode.
type debt struct {
	open, overdue, writtenOff int
	openDue, writtenOffDue    billing.Cents
}

func (d *debt) add(o debt) {
	d.open += o.open
	d.overdue += o.overdue
	d.writtenOff += o.writtenOff
	d.openDue += o.openDue
	d.writtenOffDue += o.writtenOffDue
}

func (e *Eraser) debtOf(ctx context.Context, customerID string) (debt, error) {
	var d debt
	invoices, err := e.invoices.ListByCustomer(ctx, e.portalOrg, true, customerID, 0)
	if err != nil {
		return d, err
	}
	today := brcal.Today()
	for _, inv := range invoices {
		if inv.AmountDue() <= 0 {
			continue
		}
		switch inv.Status {
		case billing.InvoiceOpen:
			d.open++
			d.openDue += inv.AmountDue()
			if inv.IsOverdue(today) {
				d.overdue++
			}
		case billing.InvoiceUncollectible:
			d.writtenOff++
			d.writtenOffDue += inv.AmountDue()
		}
	}
	return d, nil
}

// Blockers reports why sub, together with the workspaces erased with them,
// cannot be erased yet. An unknown sub and no workspaces have none. Live mode
// only: test money is not owed (R15).
func (e *Eraser) Blockers(ctx context.Context, sub string, workspaces []string) ([]erasure.Blocker, error) {
	var out []erasure.Blocker

	var tenants []string
	for _, ws := range workspaces {
		has, err := e.hasTenant(ctx, ws)
		if err != nil {
			return nil, err
		}
		if has {
			tenants = append(tenants, ws)
		}
	}
	if len(tenants) > 0 {
		out = append(out, erasure.Blocker{Code: BlockerMerchantTenant, Detail: map[string]any{"workspace_ids": tenants}})
	}
	if e.portalOrg == "" {
		return out, nil
	}

	var total debt
	var owing []string
	if c, err := e.customers.GetByUser(ctx, e.portalOrg, true, sub); err == nil {
		d, err := e.debtOf(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		total.add(d)
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}
	for _, ws := range workspaces {
		c, err := e.customers.GetByOrganization(ctx, e.portalOrg, true, ws)
		if errors.Is(err, repositories.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d, err := e.debtOf(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		if d.open+d.writtenOff > 0 {
			owing = append(owing, ws)
		}
		total.add(d)
	}
	if total.open > 0 {
		detail := map[string]any{"count": total.open, "overdue": total.overdue, "amount_cents": int64(total.openDue)}
		if len(owing) > 0 {
			detail["workspace_ids"] = owing
		}
		out = append(out, erasure.Blocker{Code: BlockerInvoiceOpen, Detail: detail, ActionURL: e.invoicesURL})
	}
	if total.writtenOff > 0 {
		// No action_url: the portal cannot collect a written-off invoice
		// (Invoice.Payable needs OPEN). Support records the payment.
		out = append(out, erasure.Blocker{
			Code:   BlockerInvoiceUncollectible,
			Detail: map[string]any{"count": total.writtenOff, "amount_cents": int64(total.writtenOffDue)},
		})
	}
	return out, nil
}

// hasTenant reports a billing tenant linked to the ctech-account workspace in
// either mode (a test-mode tenant counts: R3).
func (e *Eraser) hasTenant(ctx context.Context, workspaceID string) (bool, error) {
	for _, live := range []bool{true, false} {
		_, err := e.orgs.GetByAccountOrganization(ctx, workspaceID, live)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, repositories.ErrNotFound):
		default:
			return false, err
		}
	}
	return false, nil
}

// Purge erases m.Sub's billing data and the data of every workspace in
// m.WorkspaceIDs(). It re-checks the blockers first and never half-purges.
// Idempotent and resumable: every step re-reads and skips what is already done.
//
// Order matters. Customers are anonymized first, so the finance replay queue's
// payer side skips them (payer_anonymized) instead of writing into a space
// about to be deleted; levels and bank connections go next; the spaces last.
func (e *Eraser) Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error) {
	if e.purger == nil || e.spaceFor == nil || e.banks == nil {
		return erasure.Ack{}, errors.New("eraser: purge is not configured")
	}
	workspaces := m.WorkspaceIDs()
	blockers, err := e.Blockers(ctx, m.Sub, workspaces)
	if err != nil {
		return erasure.Ack{}, err
	}
	if len(blockers) > 0 {
		return erasure.Ack{Result: erasure.ResultBlocked, Blockers: blockers}, nil
	}

	counts := map[string]int{}
	now := time.Now()
	if e.portalOrg != "" {
		for _, live := range []bool{true, false} {
			if c, err := e.customers.GetByUser(ctx, e.portalOrg, live, m.Sub); err == nil {
				if err := e.eraseCustomer(ctx, c, m, counts, now); err != nil {
					return erasure.Ack{}, err
				}
			} else if !errors.Is(err, repositories.ErrNotFound) {
				return erasure.Ack{}, err
			}
			for _, ws := range workspaces {
				c, err := e.customers.GetByOrganization(ctx, e.portalOrg, live, ws)
				if errors.Is(err, repositories.ErrNotFound) {
					continue
				}
				if err != nil {
					return erasure.Ack{}, err
				}
				if err := e.eraseCustomer(ctx, c, m, counts, now); err != nil {
					return erasure.Ack{}, err
				}
			}
			if err := e.eraseLevels(ctx, live, m.Sub, workspaces, counts); err != nil {
				return erasure.Ack{}, err
			}
		}
	}
	for _, owner := range append([]string{"USER#" + m.Sub}, workspaces...) {
		for _, live := range []bool{true, false} {
			sp, err := e.spaceFor(owner, live)
			if err != nil {
				// A sub or workspace id that cannot name a space never had one.
				continue
			}
			if err := e.disconnectBanks(ctx, sp, m, counts, now); err != nil {
				return erasure.Ack{}, err
			}
			n, err := e.purger.PurgeSpace(ctx, sp)
			for table, c := range n {
				counts["finance_"+table] += c
			}
			if err != nil {
				return erasure.Ack{}, fmt.Errorf("purging space %s: %w", sp.PK(), err)
			}
		}
	}
	return erasure.Ack{Result: erasure.ResultDone, Counts: counts}, nil
}

// eraseCustomer cancels a tenant-zero customer's subscriptions, clears their
// metadata, and anonymizes the customer. Invoices are counted, never touched.
func (e *Eraser) eraseCustomer(ctx context.Context, c *billing.Customer, m erasure.Message, counts map[string]int, now time.Time) error {
	subs, err := e.subs.ListByCustomer(ctx, e.portalOrg, c.Livemode, c.ID, 0)
	if err != nil {
		return err
	}
	for i := range subs {
		s := &subs[i]
		if s.Status != billing.SubscriptionCanceled {
			// Immediate, not at period end: the person is leaving now. Every live
			// status has this edge under CauseCustomer (R3).
			if _, err := e.subs.Transition(ctx, s, billing.SubscriptionCanceled, billing.CauseCustomer, erasureActor, m.RequestID, now); err != nil {
				return fmt.Errorf("cancelling subscription %s: %w", s.ID, err)
			}
			counts["subscriptions_canceled"]++
		}
		if len(s.Metadata) > 0 {
			if err := e.subs.ClearMetadata(ctx, s, now); err != nil {
				return err
			}
		}
	}
	invoices, err := e.invoices.ListByCustomer(ctx, e.portalOrg, c.Livemode, c.ID, 0)
	if err != nil {
		return err
	}
	counts["invoices_retained"] += len(invoices)
	if !c.Anonymized {
		if err := e.customers.Anonymize(ctx, c, erasureActor, m.RequestID, now); err != nil {
			return err
		}
		counts["customers_anonymized"]++
	}
	return nil
}

// eraseLevels deletes the level rows of USER_{sub} and ORG_{id} for every meter
// tenant zero's catalogue declares (R21).
func (e *Eraser) eraseLevels(ctx context.Context, live bool, sub string, workspaces []string, counts map[string]int) error {
	prices, err := e.catalog.ListPrices(ctx, e.portalOrg, live, 0)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var meters []string
	for _, p := range prices {
		if p.Meter != "" && !seen[p.Meter] {
			seen[p.Meter] = true
			meters = append(meters, p.Meter)
		}
	}
	refs := []string{"USER_" + sub}
	for _, ws := range workspaces {
		refs = append(refs, repositories.OrgRefPrefix+ws)
	}
	for _, ref := range refs {
		n, err := e.levels.EraseRef(ctx, e.portalOrg, live, ref, meters)
		counts["level_rows"] += n
		if err != nil {
			return err
		}
	}
	return nil
}

// disconnectBanks disconnects every BYOK connection of the space (R23).
func (e *Eraser) disconnectBanks(ctx context.Context, sp space.ResolvedSpace, m erasure.Message, counts map[string]int, now time.Time) error {
	conns, err := e.banks.List(ctx, sp)
	if err != nil {
		return err
	}
	for _, c := range conns {
		if err := e.banks.Disconnect(ctx, sp, c.Provider, erasureActor, m.RequestID, now); err != nil {
			return fmt.Errorf("disconnecting %s in %s: %w", c.Provider, sp.PK(), err)
		}
		counts["bank_connections_disconnected"]++
	}
	return nil
}

var _ BankConnections = (*ByokConnections)(nil)

// NoBankConnections is BankConnections where BYOK is not configured: no
// connection can exist, so a purge has none to disconnect.
type NoBankConnections struct{}

func (NoBankConnections) List(context.Context, space.ResolvedSpace) ([]byokconn.Connection, error) {
	return nil, nil
}

func (NoBankConnections) Disconnect(context.Context, space.ResolvedSpace, string, string, string, time.Time) error {
	return nil
}

// ErasedSpace reports a space whose owner is locked or erased in the
// account-deletion saga: a personal space by its user, a workspace's space by
// the workspace's tombstone. It is the daily job's guard.
func ErasedSpace(store *erasure.Store) func(context.Context, space.ResolvedSpace) (bool, error) {
	return func(ctx context.Context, sp space.ResolvedSpace) (bool, error) {
		if sp.Personal() {
			return store.Blocked(ctx, strings.TrimPrefix(sp.Owner(), "USER#"))
		}
		return store.OrgErased(ctx, sp.OrganizationID())
	}
}

// ErasedRef reports a level-report customer ref whose person or workspace the
// saga has locked or erased: USER_{sub} by sub, ORG_{id} by workspace id.
func ErasedRef(store *erasure.Store) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, ref string) (bool, error) {
		switch {
		case strings.HasPrefix(ref, "USER_"):
			return store.Blocked(ctx, strings.TrimPrefix(ref, "USER_"))
		case strings.HasPrefix(ref, repositories.OrgRefPrefix):
			return store.OrgErased(ctx, strings.TrimPrefix(ref, repositories.OrgRefPrefix))
		}
		return false, nil
	}
}

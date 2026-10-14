package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var (
	// ErrNoHooksHost: this deployment has no webhook host (BYOK_HOOKS_HOST);
	// confirmation runs through the hourly sweep only.
	ErrNoHooksHost = errors.New("byok: no webhook host configured")
	// ErrTenantZeroRail: moving tenant zero's rail is the operator's audited
	// action (spec § 12.6), never a console route.
	ErrTenantZeroRail = errors.New("byok: tenant zero's collection rail is changed by the operator")
	// ErrSandboxWebhookShared: the shared sandbox key's webhook belongs to
	// every test tenant; only cmd/byok's bootstrap registers it.
	ErrSandboxWebhookShared = errors.New("byok: the shared sandbox webhook is the operator's")
)

// ErrPixKeyUnknown: the bank has no such Pix key on this account (the PUT of
// the notice is what tells it; GET answers alike for a key without one).
var ErrPixKeyUnknown = errors.New("byok: the bank does not know this Pix key")

// maxPixKeyLen is the longest Pix key there is (an e-mail address).
const maxPixKeyLen = 77

// ErrWebhookConflict: another system's URL receives the provider's payment
// notices for this key (spec § 6.4); only replace_existing overwrites it.
type ErrWebhookConflict struct{ ExistingHost string }

func (e *ErrWebhookConflict) Error() string {
	return "byok: another webhook is registered (" + e.ExistingHost + ")"
}

// ErrOpenCharges: disconnect would leave charges payable that nobody confirms.
type ErrOpenCharges struct{ Count int }

func (e *ErrOpenCharges) Error() string { return fmt.Sprintf("byok: %d open charges", e.Count) }

// DisconnectReport is what "Desconectar mesmo assim" could not do at the
// provider: the charges and the webhook the merchant must remove.
type DisconnectReport struct {
	ChargeRefs []string `json:"charge_refs"`
	WebhookURL string   `json:"webhook_url,omitempty"`
}

func (r DisconnectReport) Empty() bool { return len(r.ChargeRefs) == 0 && r.WebhookURL == "" }

// ByokCollectionSetup turns a connection into the tenant's collection rail
// and tears it down (spec § 3.6, § 6.4, § 7.1).
type ByokCollectionSetup struct {
	conns     *ByokConnections
	repo      *repositories.ByokRepository
	orgs      *repositories.OrganizationRepository
	payments  *repositories.PaymentRepository
	collector *ByokCollector
	webhooks  *ByokWebhooks
	host      string
	tenant0   string
	mail      ByokMailer
	subs      *repositories.SubscriptionRepository
}

func NewByokCollectionSetup(conns *ByokConnections, repo *repositories.ByokRepository, orgs *repositories.OrganizationRepository,
	payments *repositories.PaymentRepository, collector *ByokCollector, webhooks *ByokWebhooks, host, tenantZero string) *ByokCollectionSetup {
	return &ByokCollectionSetup{conns: conns, repo: repo, orgs: orgs, payments: payments, collector: collector, webhooks: webhooks,
		host: host, tenant0: tenantZero}
}

func (s *ByokCollectionSetup) WithMail(m ByokMailer) *ByokCollectionSetup { s.mail = m; return s }

// WithSubscriptions lets the switch onto the BYOK rail count the tenant's
// subscriptions (part C, scope decision 3).
func (s *ByokCollectionSetup) WithSubscriptions(subs *repositories.SubscriptionRepository) *ByokCollectionSetup {
	s.subs = subs
	return s
}

func hookIDOf(conn *byokconn.Connection) string {
	if conn.Kind == byokconn.KindSandboxShared {
		return repositories.SandboxHookID
	}
	return conn.HookID
}

// RegisterWebhook is spec § 6.4: GET before PUT; ours is left alone; a
// foreign URL is a conflict unless replaceExisting (audited with its host).
func (s *ByokCollectionSetup) RegisterWebhook(ctx context.Context, sp space.ResolvedSpace, provider string, replaceExisting bool, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	conn, err := s.conns.Connected(ctx, sp, provider)
	if err != nil {
		return nil, err
	}
	return conn, s.register(ctx, conn, replaceExisting, actor, requestID, now)
}

func (s *ByokCollectionSetup) register(ctx context.Context, conn *byokconn.Connection, replace bool, actor, requestID string, now time.Time) error {
	if !conn.Has(byok.CapabilityCollection) {
		return billing.ErrByokScopesMissing
	}
	if replace && conn.Kind == byokconn.KindSandboxShared {
		return ErrSandboxWebhookShared
	}
	if s.host == "" {
		return ErrNoHooksHost
	}
	sess := s.conns.Session(conn)
	current, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (string, error) {
		return sess.P.GetWebhook(ctx, cr, t, byok.ProductPix)
	})
	if err != nil {
		return err
	}
	hookID := hookIDOf(conn)
	action := "byok.webhook_registered"
	switch {
	case current != "" && IsOurHookURL(current, s.host, hookID):
		if conn.WebhookStatus == byokconn.WebhookRegistered {
			return nil
		}
		conn.WebhookStatus = byokconn.WebhookRegistered
		return s.repo.SaveConnection(ctx, conn, actor, requestID, action, now)
	case current != "" && !replace:
		conn.WebhookStatus = byokconn.WebhookConflict
		if err := s.repo.SaveConnection(ctx, conn, actor, requestID, "byok.webhook_conflict", now); err != nil {
			return err
		}
		return &ErrWebhookConflict{ExistingHost: hostOf(current)}
	case current != "":
		action = "byok.webhook_replaced"
		slog.InfoContext(ctx, "byok webhook replaced", "connection_id", conn.ID, "replaced_host", hostOf(current), "actor", actor)
	}
	id, secret, err := s.webhooks.IssueHook(ctx, conn, hookID, now)
	if err != nil {
		return err
	}
	if err := pix(ctx, sess); err != nil {
		return err
	}
	if _, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (struct{}, error) {
		return struct{}{}, sess.P.RegisterWebhook(ctx, cr, t, byok.ProductPix, HookURL(s.host, conn.Provider, id, secret, string(byok.ProductPix)))
	}); err != nil {
		return err
	}
	if conn.Kind == byokconn.KindMerchant {
		conn.HookID = id
	}
	conn.WebhookStatus = byokconn.WebhookRegistered
	return s.repo.SaveConnection(ctx, conn, actor, requestID, action, now)
}

// ChangePixKey moves a merchant connection to another Pix key of the same
// account: the key is not part of the sealed credentials, so no secret or
// certificate is involved. Charges still payable pay into the old key, so
// they must be settled or cancelled first. The notice follows the key (a
// foreign URL on it is a conflict, replaced only on confirmation, spec § 6.4);
// Billing's own notice on the old key is removed best effort. Inter keeps one
// URL per key, which is the reason to give each consumer a key of its own.
func (s *ByokCollectionSetup) ChangePixKey(ctx context.Context, sp space.ResolvedSpace, provider, pixKey string, replaceExisting bool, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	pixKey = strings.TrimSpace(pixKey)
	if pixKey == "" || len(pixKey) > maxPixKeyLen {
		return nil, fmt.Errorf("%w: pix_key", byok.ErrInvalidRequest)
	}
	conn, err := s.conns.Connected(ctx, sp, provider)
	if err != nil {
		return nil, err
	}
	if conn.Kind == byokconn.KindSandboxShared {
		return nil, ErrSandboxWebhookShared
	}
	if pixKey == conn.PixKey {
		return conn, nil
	}
	tenant, err := s.orgs.GetByAccountOrganization(ctx, conn.AccountOrganizationID, conn.Livemode)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}
	if tenant != nil {
		open, err := s.openCharges(ctx, tenant, provider)
		if err != nil {
			return nil, err
		}
		if len(open) > 0 {
			return nil, &ErrOpenCharges{Count: len(open)}
		}
	}
	old := *conn
	next := *conn
	next.PixKey = pixKey
	if err := s.register(ctx, &next, replaceExisting, actor, requestID, now); err != nil {
		if errors.Is(err, byok.ErrNotFound) {
			return nil, ErrPixKeyUnknown
		}
		var conflict *ErrWebhookConflict
		if errors.As(err, &conflict) {
			// register saved the conflict on the new key; the change stands.
			_ = s.repo.SaveConnection(ctx, &next, actor, requestID, "byok.pix_key_changed", now)
		}
		return nil, err
	}
	if err := s.repo.SaveConnection(ctx, &next, actor, requestID, "byok.pix_key_changed", now); err != nil {
		return nil, err
	}
	if old.HookID != "" && s.host != "" {
		if _, err := s.deleteOurWebhook(ctx, &old); err != nil {
			slog.WarnContext(ctx, "byok old pix key notice not removed", "connection_id", old.ID, "error", err)
		}
	}
	return &next, nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "?"
	}
	return u.Hostname()
}

// UseForCollection makes a collection-capable connection the tenant's rail
// (spec § 7.1 as amended: enabled at once, live as test) with its late fees,
// and registers the webhook; a conflict or a provider failure leaves the
// sweep confirming within the hour (the console says so).
func (s *ByokCollectionSetup) UseForCollection(ctx context.Context, sp space.ResolvedSpace, tenant *billing.Organization, provider string,
	settings billing.CollectionSettings, actor, requestID string, now time.Time) error {
	if s.tenant0 != "" && tenant.ID == s.tenant0 {
		return ErrTenantZeroRail
	}
	return s.useForCollection(ctx, sp, tenant, provider, settings, actor, requestID, now)
}

// OperatorUseForCollection is the same move for tenant zero (spec § 12.6): CTech
// billing its own customers through its own bank connection. It is not
// reachable from any route; cmd/byok -tenant-rail calls it, so the move is
// always an audited action by a named operator.
func (s *ByokCollectionSetup) OperatorUseForCollection(ctx context.Context, sp space.ResolvedSpace, tenant *billing.Organization, provider string,
	settings billing.CollectionSettings, actor, requestID string, now time.Time) error {
	return s.useForCollection(ctx, sp, tenant, provider, settings, actor, requestID, now)
}

// OperatorLeaveCollection puts a tenant back on the wallet rail, still
// collecting (the gate stays enabled): the way back from OperatorUseForCollection.
// Open BYOK charges stay payable and the sweep keeps confirming them.
func (s *ByokCollectionSetup) OperatorLeaveCollection(ctx context.Context, tenant *billing.Organization, actor, requestID string, now time.Time) error {
	return s.orgs.SetPayoutRail(ctx, tenant, billing.RailWallet, "", billing.PayoutEnabled, actor, requestID, now)
}

func (s *ByokCollectionSetup) useForCollection(ctx context.Context, sp space.ResolvedSpace, tenant *billing.Organization, provider string,
	settings billing.CollectionSettings, actor, requestID string, now time.Time) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	conn, err := s.conns.Connected(ctx, sp, provider)
	if errors.Is(err, ErrByokNotConnected) {
		return billing.ErrByokNotConnected
	}
	if err != nil {
		return err
	}
	if !conn.Has(byok.CapabilityCollection) {
		return billing.ErrByokScopesMissing
	}
	if tenant.Collection != settings {
		if err := s.orgs.SetCollection(ctx, tenant, settings, actor, requestID, now); err != nil {
			return err
		}
	}
	if tenant.Rail() != billing.RailBYOK || tenant.PayoutProvider != provider || tenant.PayoutStatus != billing.PayoutEnabled {
		if err := s.orgs.SetPayoutRail(ctx, tenant, billing.RailBYOK, provider, billing.PayoutEnabled, actor, requestID, now); err != nil {
			return err
		}
		if s.subs != nil && tenant.Livemode {
			n, err := s.subs.CountOpen(ctx, tenant.ID, true)
			if err != nil {
				return err
			}
			if err := s.repo.InitSubscriptionCount(ctx, tenant.ID, n); err != nil {
				return err
			}
		}
	}
	if conn.WebhookStatus != byokconn.WebhookRegistered {
		if err := s.register(ctx, conn, false, actor, requestID, now); err != nil {
			slog.WarnContext(ctx, "byok webhook not registered; the sweep confirms", "connection_id", conn.ID, "reason", err.Error())
		}
	}
	return nil
}

// Disconnect is spec § 3.6 in order: open charges (refused without
// cancelOpen), our webhook (a foreign one never), credentials and the hook
// row, the gate, the notice. force skips provider calls that fail as
// unavailable and reports what the merchant must remove.
func (s *ByokCollectionSetup) Disconnect(ctx context.Context, sp space.ResolvedSpace, provider string, cancelOpen, force bool, actor, requestID string, now time.Time) (DisconnectReport, error) {
	var rep DisconnectReport
	conn, err := s.conns.Get(ctx, sp, provider)
	if err != nil {
		return rep, err
	}
	if conn.State == "" {
		return rep, s.conns.Disconnect(ctx, sp, provider, actor, requestID, now)
	}
	tenant, err := s.orgs.GetByAccountOrganization(ctx, conn.AccountOrganizationID, conn.Livemode)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return rep, err
	}
	if tenant != nil {
		open, err := s.openCharges(ctx, tenant, provider)
		if err != nil {
			return rep, err
		}
		if len(open) > 0 && !cancelOpen {
			return rep, &ErrOpenCharges{Count: len(open)}
		}
		for i := range open {
			paid, err := s.collector.cancelAt(ctx, tenant, &open[i], "byok_disconnected", billing.CauseByokDisconnected, now)
			switch {
			case err == nil:
				_ = paid
			case force && errors.Is(err, byok.ErrUnavailable):
				rep.ChargeRefs = append(rep.ChargeRefs, open[i].ChargeRef)
			default:
				return rep, err
			}
		}
	}
	if conn.Kind == byokconn.KindMerchant && conn.HookID != "" && s.host != "" {
		if u, err := s.deleteOurWebhook(ctx, conn); err != nil {
			if !force || !errors.Is(err, byok.ErrUnavailable) {
				return rep, err
			}
			rep.WebhookURL = u
		}
	}
	if err := s.conns.Disconnect(ctx, sp, provider, actor, requestID, now); err != nil {
		return rep, err
	}
	if conn.Kind == byokconn.KindMerchant {
		if err := s.repo.DeleteHook(ctx, conn.HookID); err != nil {
			return rep, err
		}
	}
	if tenant != nil && tenant.Rail() == billing.RailBYOK && tenant.PayoutProvider == provider {
		if err := s.orgs.SetPayoutRail(ctx, tenant, "", "", billing.PayoutNotConfigured, actor, requestID, now); err != nil {
			return rep, err
		}
	}
	if s.mail != nil && conn.NotifyEmail != "" {
		if err := s.mail.SendByokNotice(ctx, conn.NotifyEmail, email.ByokNotice{Kind: "disconnected", Provider: provider}); err != nil {
			slog.WarnContext(ctx, "byok disconnect notice", "connection_id", conn.ID, "error", err)
		}
	}
	return rep, nil
}

// deleteOurWebhook deletes the registered webhook only when it is ours; it
// returns the URL the merchant must remove when the provider is down.
func (s *ByokCollectionSetup) deleteOurWebhook(ctx context.Context, conn *byokconn.Connection) (string, error) {
	sess := s.conns.Session(conn)
	ourPrefix := "https://" + s.host + "/w/" + conn.Provider + "/" + conn.HookID + "/"
	current, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (string, error) {
		return sess.P.GetWebhook(ctx, cr, t, byok.ProductPix)
	})
	if err != nil {
		return ourPrefix, err
	}
	if current == "" || !IsOurHookURL(current, s.host, conn.HookID) {
		return "", nil
	}
	_, err = byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (struct{}, error) {
		return struct{}{}, sess.P.DeleteWebhook(ctx, cr, t, byok.ProductPix)
	})
	return ourPrefix, err
}

// openCharges is the tenant's pending BYOK attempts on this provider.
func (s *ByokCollectionSetup) openCharges(ctx context.Context, tenant *billing.Organization, provider string) ([]billing.PaymentAttempt, error) {
	var out []billing.PaymentAttempt
	var start map[string]types.AttributeValue
	for {
		page, err := s.payments.OpenByok(ctx, tenant.ID, tenant.Livemode, start, 100)
		if err != nil {
			return nil, err
		}
		for _, a := range page.Items {
			if a.Provider == provider {
				out = append(out, a)
			}
		}
		if page.LastEvaluatedKey == nil {
			return out, nil
		}
		start = page.LastEvaluatedKey
	}
}

package services

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var (
	ErrByokSpaceKind        = errors.New("byok: only organization workspaces connect")
	ErrConnectNotAuthorized = errors.New("byok: the authorization must be accepted")
	ErrSandboxInLive        = errors.New("byok: the shared sandbox is test mode only")
	ErrBackfillRange        = errors.New("byok: backfill_months is 3 to 12")
	// ErrByokNotConnected is a known provider the organization has not
	// connected (or only started onboarding with). 404 byok_not_connected.
	ErrByokNotConnected = errors.New("byok: not connected")
)

const OnboardingSteps = "requirements,create_integration,validation,download,upload,webhook"

// Providers known in phase 1. An unknown provider is a 404 at the route.
var KnownProviders = []string{"inter"}

type ConnectInput struct {
	Provider                                string
	ClientID, ClientSecret, CertPEM, KeyPEM string
	Account, PixKey, NotifyEmail            string
	BackfillMonths                          int
	Authorized                              bool
}

func (in *ConnectInput) validate() error {
	if in.BackfillMonths == 0 {
		in.BackfillMonths = 3
	}
	if in.BackfillMonths < 3 || in.BackfillMonths > 12 {
		return ErrBackfillRange
	}
	if !in.Authorized {
		return ErrConnectNotAuthorized
	}
	if in.ClientID == "" || in.ClientSecret == "" || strings.TrimLeft(in.Account, "0") == "" || !digits(in.Account) {
		return fmt.Errorf("%w: client_id, client_secret and account are required", byok.ErrInvalidRequest)
	}
	if _, err := mail.ParseAddress(in.NotifyEmail); err != nil {
		return fmt.Errorf("%w: notify_email", byok.ErrInvalidRequest)
	}
	in.Account = strings.TrimLeft(in.Account, "0")
	return nil
}

func digits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// checkPair is the api's own PEM check (spec § 3.2 item 1), so an obviously
// wrong upload never reaches the Lambda. Nothing is kept.
func checkPair(certPEM, keyPEM string, now time.Time) error {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return byok.ErrCertificateInvalid
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return byok.ErrCertificateInvalid
	}
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		if k, _ := pem.Decode([]byte(keyPEM)); k == nil {
			return byok.ErrCertificateInvalid
		}
		return byok.ErrKeyMismatch
	}
	if !now.Before(cert.NotAfter) {
		return byok.ErrCertificateExpired
	}
	return nil
}

func LedgerAccountID(provider, account string) string {
	sum := sha256.Sum256([]byte(provider + ":" + account))
	return "byok-" + provider + "-" + hex.EncodeToString(sum[:6])
}

func ledgerAccountName(provider, account string) string {
	tail := account
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return strings.ToUpper(provider[:1]) + provider[1:] + " ···" + tail
}

type ByokConnections struct {
	repo      *repositories.ByokRepository
	ledger    *repositories.LedgerRepository
	out       *byokrpc.Outbound
	tokens    *byokrpc.TokenManager
	limits    *byokrpc.Limits
	statement *ByokStatement
}

func NewByokConnections(repo *repositories.ByokRepository, ledger *repositories.LedgerRepository, out *byokrpc.Outbound,
	tokens *byokrpc.TokenManager, limits *byokrpc.Limits, statement *ByokStatement) *ByokConnections {
	return &ByokConnections{repo: repo, ledger: ledger, out: out, tokens: tokens, limits: limits, statement: statement}
}

func (s *ByokConnections) Session(conn *byokconn.Connection) byokrpc.Session {
	return byokrpc.Session{P: s.out.For(conn.Provider, conn.Livemode), Conn: conn, Tokens: s.tokens, Limits: s.limits}
}

func requireOrg(sp space.ResolvedSpace) error {
	if sp.Personal() || sp.Kind() != space.KindOrganization {
		return ErrByokSpaceKind
	}
	return sp.Require(space.Configure)
}

// Get returns the provider's row, an onboarding-only row (State "") included,
// so the guided onboarding can show where the person stopped.
func (s *ByokConnections) Get(ctx context.Context, sp space.ResolvedSpace, provider string) (*byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	conn, err := s.repo.GetConnection(ctx, sp.OrganizationID(), sp.Livemode(), provider)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, ErrByokNotConnected
	}
	return conn, err
}

// Connected is Get for an action: an onboarding-only row has no credentials.
func (s *ByokConnections) Connected(ctx context.Context, sp space.ResolvedSpace, provider string) (*byokconn.Connection, error) {
	conn, err := s.Get(ctx, sp, provider)
	if err != nil {
		return nil, err
	}
	if conn.State == "" {
		return nil, ErrByokNotConnected
	}
	return conn, nil
}

func (s *ByokConnections) List(ctx context.Context, sp space.ResolvedSpace) ([]byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	return s.repo.ListConnections(ctx, sp.OrganizationID(), sp.Livemode())
}

// Connect (live only): checks, Lambda Connect (test + seal), the row, the
// Finanças account, then the first backfill window in the background.
func (s *ByokConnections) Connect(ctx context.Context, sp space.ResolvedSpace, in ConnectInput, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if !sp.Livemode() {
		return nil, fmt.Errorf("%w: test mode uses the shared sandbox (PUT with an empty body)", byok.ErrInvalidRequest)
	}
	if err := checkPair(in.CertPEM, in.KeyPEM, now); err != nil {
		return nil, err
	}
	conn := &byokconn.Connection{AccountOrganizationID: sp.OrganizationID(), Livemode: true, Provider: in.Provider,
		ID: id.New(), Kind: byokconn.KindMerchant, Version: 1, ClientID: in.ClientID, AccountNumber: in.Account,
		PixKey: in.PixKey, NotifyEmail: in.NotifyEmail}
	got, err := s.out.For(in.Provider, true).Connect(ctx, byok.Credentials{
		Ref:     &byok.ConnectionRef{AccountOrganizationID: conn.AccountOrganizationID, ID: conn.ID, Version: 1},
		Values:  map[string]string{"client_id": in.ClientID, "client_secret": in.ClientSecret, "cert_pem": in.CertPEM, "key_pem": in.KeyPEM},
		Account: in.Account, PixKey: in.PixKey,
	})
	if err != nil {
		return nil, err
	}
	s.apply(conn, got, now)
	conn.State, conn.SyncState = byokconn.StateActive, byokconn.SyncActive
	conn.AuthorizedBy, conn.AuthorizedAt, conn.AuthorizationVersion = actor, now.UTC(), byokconn.AuthorizationVersion
	conn.OnboardingStep, conn.OnboardingSince = "webhook", now.UTC()
	today := brcal.FromTime(now)
	conn.FirstDay = today.AddMonths(-in.BackfillMonths)
	conn.BackfillFrom = today
	return s.create(ctx, sp, conn, actor, requestID, now)
}

func (s *ByokConnections) apply(conn *byokconn.Connection, got byok.Connected, now time.Time) {
	conn.Sealed = got.Sealed
	conn.Capabilities, conn.ScopesGranted, conn.MissingScopes = got.Capabilities, got.ScopesGranted, got.MissingScopes
	conn.CertSubject, conn.CertFingerprint, conn.CertNotAfter = got.CertSubject, got.CertFingerprint, got.CertNotAfter
	conn.LastTokenOKAt, conn.ConsecutiveFailures, conn.LastErrorCode = now.UTC(), 0, ""
}

func (s *ByokConnections) create(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	accountKey := conn.AccountNumber
	if conn.Kind == byokconn.KindSandboxShared {
		accountKey = "sandbox"
	}
	conn.LedgerAccountID = LedgerAccountID(conn.Provider, accountKey)
	created, err := s.ledger.EnsureByokAccount(ctx, sp, conn.LedgerAccountID, ledgerAccountName(conn.Provider, accountKey), conn.Provider, now)
	if err != nil {
		return nil, err
	}
	if !created {
		// The same bank account again (spec § 3.6 step 5): it resumes on its
		// ledger account. Its opening already counts everything before the old
		// first day, so the new backfill never reaches back past it; the lines
		// after it are deduplicated by their FITID# locks (final review I4).
		if err := s.ledger.SetSyncState(ctx, sp, conn.LedgerAccountID, string(byokconn.SyncActive)); err != nil {
			return nil, err
		}
		if opened, ok, err := s.ledger.OpeningDate(ctx, sp, conn.LedgerAccountID); err != nil {
			return nil, err
		} else if ok && !opened.Before(conn.FirstDay) {
			conn.FirstDay = opened.AddDays(1)
		}
	}
	if err := s.repo.CreateConnection(ctx, conn, actor, requestID, now); err != nil {
		return nil, err
	}
	if !conn.BackfillDone { // the shared sandbox has no history to backfill
		s.statement.StartBackfill(sp, conn, now) // detached, bounded, under the connection lock
	}
	return conn, nil
}

// EnableSandbox is "Cobrança via Inter (teste)" / the test-mode statement:
// no credentials, CTech's own integration against the sandbox host (§ 9 as amended).
func (s *ByokConnections) EnableSandbox(ctx context.Context, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	if err := requireOrg(sp); err != nil {
		return nil, err
	}
	if sp.Livemode() {
		return nil, ErrSandboxInLive
	}
	if existing, err := s.repo.GetConnection(ctx, sp.OrganizationID(), false, provider); err == nil && existing.State != "" {
		return existing, nil // a double click
	}
	conn := &byokconn.Connection{AccountOrganizationID: sp.OrganizationID(), Provider: provider, ID: id.New(),
		Kind: byokconn.KindSandboxShared, State: byokconn.StateActive, SyncState: byokconn.SyncActive, Version: 1,
		Capabilities: []byok.Capability{byok.CapabilityStatement, byok.CapabilityCollection},
		AuthorizedBy: actor, AuthorizedAt: now.UTC(), AuthorizationVersion: byokconn.AuthorizationVersion,
		FirstDay: brcal.FromTime(now), BackfillFrom: brcal.FromTime(now), BackfillDone: true}
	return s.create(ctx, sp, conn, actor, requestID, now)
}

// Test re-runs a token fetch and a balance read, records health and answers it.
func (s *ByokConnections) Test(ctx context.Context, sp space.ResolvedSpace, provider string, now time.Time) (*byokconn.Connection, error) {
	conn, err := s.Connected(ctx, sp, provider)
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, conn)
	_, err = s.Session(conn).Balance(ctx, byok.Date{})
	s.RecordOutcome(ctx, conn, err, now)
	if err != nil {
		return conn, err
	}
	return conn, nil
}

// Renew: § 3.2 checks, Reseal (the api never sees the client_secret again),
// one conditional write of the new version; warnings reset.
func (s *ByokConnections) Renew(ctx context.Context, sp space.ResolvedSpace, provider, certPEM, keyPEM, actor, requestID string, now time.Time) (*byokconn.Connection, error) {
	conn, err := s.Connected(ctx, sp, provider)
	if err != nil {
		return nil, err
	}
	if conn.Kind != byokconn.KindMerchant {
		return nil, ErrSandboxInLive
	}
	if err := checkPair(certPEM, keyPEM, now); err != nil {
		return nil, err
	}
	got, err := s.out.For(provider, conn.Livemode).Reseal(ctx, conn.Ref(), certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	old := *conn
	s.apply(conn, got, now)
	conn.Version++
	conn.State = byokconn.StateActive
	conn.ResetWarnings()
	if err := s.repo.SaveConnection(ctx, conn, actor, requestID, "byok.certificate_renewed", now); err != nil {
		return nil, err
	}
	s.tokens.Invalidate(ctx, &old)
	return conn, nil
}

func (s *ByokConnections) SetOnboarding(ctx context.Context, sp space.ResolvedSpace, provider, step string, now time.Time) error {
	if err := requireOrg(sp); err != nil {
		return err
	}
	if !slices.Contains(strings.Split(OnboardingSteps, ","), step) {
		return fmt.Errorf("%w: step", byok.ErrInvalidRequest)
	}
	return s.repo.PutOnboarding(ctx, sp.OrganizationID(), sp.Livemode(), provider, step, now)
}

// Disconnect, part A: credentials and tokens deleted, the Finanças account
// frozen. Part B prepends open charges and the webhook (spec § 3.6 steps 1-2).
func (s *ByokConnections) Disconnect(ctx context.Context, sp space.ResolvedSpace, provider, actor, requestID string, now time.Time) error {
	conn, err := s.Get(ctx, sp, provider)
	if err != nil {
		return err
	}
	if conn.State == "" { // only onboarding progress: nothing to tear down
		return s.repo.DeleteConnection(ctx, conn, actor, requestID, now)
	}
	s.invalidate(ctx, conn)
	if err := s.repo.DeleteConnection(ctx, conn, actor, requestID, now); err != nil {
		return err
	}
	return s.ledger.SetSyncState(ctx, sp, conn.LedgerAccountID, string(byokconn.SyncDisconnected))
}

// RecordOutcome keeps the § 3.4 health fields. A refused call never marks
// anything paid; it only counts.
func (s *ByokConnections) RecordOutcome(ctx context.Context, conn *byokconn.Connection, err error, now time.Time) {
	switch {
	case err == nil:
		conn.ConsecutiveFailures, conn.LastErrorCode, conn.LastTokenOKAt = 0, "", now.UTC()
	case errors.Is(err, byok.ErrUnauthorized), errors.Is(err, byok.ErrCredentialsRefused), errors.Is(err, byok.ErrUnavailable):
		conn.ConsecutiveFailures++
		conn.LastErrorCode, conn.LastErrorAt = byok.ErrorCode(err), now.UTC()
	default:
		return // a caller error (rate limit, invalid request) says nothing about health
	}
	_ = s.repo.SaveConnection(ctx, conn, "system", "", "", now) // a lost race: the other writer recorded health too
}

// SandboxCertNotAfter is the shared sandbox integration's certificate expiry,
// as the outbound Lambda reports it with a token (a cached token carries it).
func (s *ByokConnections) SandboxCertNotAfter(ctx context.Context, conn *byokconn.Connection) (time.Time, error) {
	tok, err := s.tokens.Bearer(ctx, s.Session(conn).P, conn, byok.CapabilityStatement)
	if err != nil {
		return time.Time{}, err
	}
	return tok.CertNotAfter, nil
}

// invalidate drops a connection's cached tokens. Never the shared sandbox's:
// that token and its fetch limit belong to every test tenant together, and one
// tenant's test or disconnect must not cost the others (final review I3).
func (s *ByokConnections) invalidate(ctx context.Context, conn *byokconn.Connection) {
	if conn.Kind == byokconn.KindSandboxShared {
		return
	}
	s.tokens.Invalidate(ctx, conn)
}

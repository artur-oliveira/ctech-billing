package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/oauthclients"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// CredentialConnector admits a merchant's own ctech-account OAuth client as the
// tenant's API credential (console self-service spec § 5.1, D5).
//
// The client secret never passes through billing: the merchant creates the
// client on ctech-account's developer page and gives billing its id; billing
// asks ctech-account who owns it and what it holds, and admits it only when a
// client_credentials token for it would be issued and would act for this
// tenant alone.
type CredentialConnector struct {
	clients OAuthClientReader
	creds   CredentialStore
	machine map[string]bool
	sorted  []string
}

// OAuthClientReader is ctech-account's read of one client (api-commons
// oauthclients).
type OAuthClientReader interface {
	Get(ctx context.Context, clientID string) (oauthclients.OAuthClient, error)
}

// CredentialStore is the slice of the credential repository the rules need.
type CredentialStore interface {
	Resolve(ctx context.Context, clientID string) (*billing.APICredential, error)
	Get(ctx context.Context, organizationID string, livemode bool, clientID string) (*billing.APICredential, error)
	Connect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error
}

// ConnectRefusal is why a client was not admitted; the value is the wire code.
type ConnectRefusal string

const (
	// RefusedNotYours: unknown, or owned by somebody else. One answer for both,
	// so the route is no oracle for which client ids exist.
	RefusedNotYours ConnectRefusal = "client_not_yours"
	// RefusedNoBillingScope: the client holds no billing: scope at all.
	RefusedNoBillingScope ConnectRefusal = "client_has_no_billing_scope"
	// RefusedNotMachineOnly: it holds a scope billing does not mark m2m (a
	// sign-in scope, a user scope, another product's or an internal one), or it
	// cannot authenticate as itself (inactive). ctech-account would never issue
	// it a client_credentials token, so admitting it would admit nothing.
	RefusedNotMachineOnly ConnectRefusal = "client_not_machine_only"
	// RefusedAlreadyConnected: admitted to a tenant (any, never said which).
	RefusedAlreadyConnected ConnectRefusal = "client_already_connected"
)

// ConnectRefusedError carries a refusal; the handler answers it as a 422.
type ConnectRefusedError struct{ Code ConnectRefusal }

func (e *ConnectRefusedError) Error() string { return "client not admitted: " + string(e.Code) }

// ErrAccountUnavailable is ctech-account not answering about the client. It is
// never a refusal: the client may be perfectly good.
var ErrAccountUnavailable = errors.New("ctech-account could not be asked about the client")

// ConnectRequest is one connect, as the console resolved it.
type ConnectRequest struct {
	OrganizationID string
	Livemode       bool
	ClientID       string
	UserID         string
	RequestID      string
	Now            time.Time
}

func NewCredentialConnector(clients OAuthClientReader, creds CredentialStore, machineScopes []string) *CredentialConnector {
	set := make(map[string]bool, len(machineScopes))
	for _, s := range machineScopes {
		set[s] = true
	}
	sorted := slices.Clone(machineScopes)
	slices.Sort(sorted)
	return &CredentialConnector{clients: clients, creds: creds, machine: set, sorted: sorted}
}

// MachineScopes is the m2m set, sorted: what the console's link to the
// developer page preselects.
func (s *CredentialConnector) MachineScopes() []string { return slices.Clone(s.sorted) }

func refuse(code ConnectRefusal) (*billing.APICredential, bool, error) {
	return nil, false, &ConnectRefusedError{Code: code}
}

// Connect applies the rules in an order that leaks nothing: ownership first, so
// only the client's owner ever learns it is connected somewhere.
func (s *CredentialConnector) Connect(ctx context.Context, r ConnectRequest) (*billing.APICredential, bool, error) {
	oc, err := s.clients.Get(ctx, r.ClientID)
	switch {
	case errors.Is(err, oauthclients.ErrNotFound):
		return refuse(RefusedNotYours)
	case err != nil:
		return nil, false, fmt.Errorf("%w: %v", ErrAccountUnavailable, err)
	}
	if oc.OwnerUserID == "" || oc.OwnerUserID != r.UserID {
		return refuse(RefusedNotYours)
	}
	if code := s.scopeVerdict(oc.AllowedScopes); code != "" {
		return refuse(code)
	}
	if !oc.Active {
		return refuse(RefusedNotMachineOnly)
	}

	existing, err := s.creds.Resolve(ctx, r.ClientID)
	switch {
	case err == nil:
		if existing.OrganizationID == r.OrganizationID && existing.Livemode == r.Livemode {
			return existing, false, nil
		}
		return refuse(RefusedAlreadyConnected)
	case errors.Is(err, billing.ErrCredentialInactive):
		// Billing's own kill switch is on for it somewhere: still taken.
		return refuse(RefusedAlreadyConnected)
	case !errors.Is(err, repositories.ErrNotFound):
		return nil, false, err
	}

	cred := &billing.APICredential{
		ClientID: r.ClientID, OrganizationID: r.OrganizationID, Livemode: r.Livemode, Active: true,
		ConnectedBy: r.UserID, ConnectedAt: r.Now.UTC().Format(time.RFC3339),
	}
	if err := s.creds.Connect(ctx, cred, "user:"+r.UserID, r.RequestID, r.Now); err != nil {
		if errors.Is(err, repositories.ErrCredentialTaken) {
			// The lookup index lags a fresh write, so the second request of a
			// double click can miss the first one's row and lose on the guard.
			// A read of this tenant's own row (strongly consistent) tells that
			// apart from another tenant holding the client.
			if here, getErr := s.creds.Get(ctx, r.OrganizationID, r.Livemode, r.ClientID); getErr == nil {
				return here, false, nil
			}
			return refuse(RefusedAlreadyConnected)
		}
		return nil, false, err
	}
	return cred, true, nil
}

func (s *CredentialConnector) scopeVerdict(scopes []string) ConnectRefusal {
	if !slices.ContainsFunc(scopes, func(sc string) bool { return strings.HasPrefix(sc, "billing:") }) {
		return RefusedNoBillingScope
	}
	for _, sc := range scopes {
		if !s.machine[sc] {
			return RefusedNotMachineOnly
		}
	}
	return ""
}

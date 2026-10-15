package realtime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/accountorgs"
	"gopkg.aoctech.app/api-commons/jwtverify"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ids are canonical lower-case UUIDs: the space resolver refuses anything else.
const (
	acctA = "01a04ed6-1af9-745e-bcf7-d3b66fe52321"
	acctB = "01b04ed6-1af9-745e-bcf7-d3b66fe52322"
	orgA  = "ctech-a"
	orgB  = "ctech-b"
)

var testNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// jwtLike is what the fake verifier hands back for a token: a JWT-shaped string
// whose payload carries exp, because the authorizer reads the expiry from the
// payload of a token that has already been verified.
func jwtLike(exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	return "e30." + payload + ".sig"
}

type fakeVerifier struct{ claims map[string]*jwtverify.Claims }

func (f fakeVerifier) VerifyClaims(_ context.Context, token string) (*jwtverify.Claims, error) {
	if c, ok := f.claims[token]; ok {
		return c, nil
	}
	return nil, errors.New("bad token")
}

type fakeMembers map[string]accountorgs.Membership // "org|user"

func (f fakeMembers) Membership(_ context.Context, org, user string) (accountorgs.Membership, error) {
	return f[org+"|"+user], nil
}

type fakeTenants struct{}

func (fakeTenants) GetByAccountOrganization(_ context.Context, acct string, live bool) (*billing.Organization, error) {
	switch acct {
	case acctA:
		return &billing.Organization{ID: orgA, Livemode: live}, nil
	case acctB:
		return &billing.Organization{ID: orgB, Livemode: live}, nil
	}
	return nil, repositories.ErrNotFound
}

type fixture struct {
	console  *ConsoleAuthorizer
	portal   *PortalAuthorizer
	checkout *CheckoutAuthorizer
	links    *services.PayLink
	tokens   map[string]string // name -> token
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	exp := testNow.Add(10 * time.Minute)
	tokens := map[string]string{
		"s1": jwtLike(exp) + "1", "s2": jwtLike(exp) + "2", "u1": jwtLike(exp) + "u1", "u2": jwtLike(exp) + "u2",
		"m2m": jwtLike(exp) + "m2m", "viewer": jwtLike(exp) + "viewer",
	}
	claims := map[string]*jwtverify.Claims{
		tokens["s1"]: {Sub: "s1", SID: "sid"}, tokens["s2"]: {Sub: "s2", SID: "sid"},
		tokens["u1"]: {Sub: "u1", SID: "sid"}, tokens["u2"]: {Sub: "u2", SID: "sid"},
		tokens["m2m"]: {Sub: "client-1"}, tokens["viewer"]: {Sub: "viewer", SID: "sid"},
	}
	verifier := fakeVerifier{claims: claims}
	members := fakeMembers{
		acctA + "|s1":     {Member: true, Role: "admin", Kind: "organization"},
		acctB + "|s2":     {Member: true, Role: "owner", Kind: "organization"},
		acctA + "|viewer": {Member: true, Role: "viewer", Kind: "organization"},
	}
	links := services.NewPayLink("a-secret-of-at-least-thirty-two-bytes!", "https://pay.example")
	now := func() time.Time { return testNow }
	return &fixture{
		console:  &ConsoleAuthorizer{Verifier: verifier, Spaces: space.NewResolver(members, nil), Tenants: fakeTenants{}, Now: now},
		portal:   &PortalAuthorizer{Verifier: verifier, Now: now},
		checkout: &CheckoutAuthorizer{Links: links, Lifetime: 35 * time.Minute, Now: now},
		links:    links,
		tokens:   tokens,
	}
}

var allScopes = []string{
	TenantScope(orgA, true), TenantScope(orgA, false), TenantScope(orgB, true),
	SpaceScope(acctA + "#live"), SpaceScope(acctA + "#test"), SpaceScope(acctB + "#live"),
	UserScope("u1"), UserScope("u2"),
	InvoiceScope(orgA, true, "in_1"), InvoiceScope(orgB, true, "in_2"),
}

type named struct {
	name      string
	principal *Principal
}

func principals(t *testing.T) []named {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()
	must := func(name string, p *Principal, err error) named {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return named{name, p}
	}
	var out []named
	p, err := f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s1"], Space: "org:" + acctA, Mode: "live"})
	out = append(out, must("console A live", p, err))
	p, err = f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s1"], Space: "org:" + acctA, Mode: "test"})
	out = append(out, must("console A test", p, err))
	p, err = f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s2"], Space: "org:" + acctB, Mode: "live"})
	out = append(out, must("console B live", p, err))
	p, err = f.portal.Authorize(ctx, &wire.Auth{Token: f.tokens["u1"]})
	out = append(out, must("portal U1", p, err))
	p, err = f.portal.Authorize(ctx, &wire.Auth{Token: f.tokens["u2"]})
	out = append(out, must("portal U2", p, err))
	p, err = f.checkout.Authorize(ctx, &wire.Auth{PayLink: f.links.Sign(orgA, true, "in_1")})
	out = append(out, must("checkout 1", p, err))
	p, err = f.checkout.Authorize(ctx, &wire.Auth{PayLink: f.links.Sign(orgB, true, "in_2")})
	out = append(out, must("checkout 2", p, err))
	return out
}

// owns is the diagonal of the matrix, written out by hand rather than computed
// from the code under test.
func owns(name, scope string) bool {
	want := map[string][]string{
		"console A live": {TenantScope(orgA, true), SpaceScope(acctA + "#live")},
		"console A test": {TenantScope(orgA, false), SpaceScope(acctA + "#test")},
		"console B live": {TenantScope(orgB, true), SpaceScope(acctB + "#live")},
		"portal U1":      {UserScope("u1")},
		"portal U2":      {UserScope("u2")},
		"checkout 1":     {InvoiceScope(orgA, true, "in_1")},
		"checkout 2":     {InvoiceScope(orgB, true, "in_2")},
	}
	return slices.Contains(want[name], scope)
}

func TestNoPrincipalIsGrantedAScopeItDoesNotOwn(t *testing.T) {
	for _, p := range principals(t) {
		for _, s := range allScopes {
			if got, want := slices.Contains(p.principal.Scopes, s), owns(p.name, s); got != want {
				t.Errorf("%s / %s: granted=%v, want %v", p.name, s, got, want)
			}
		}
	}
}

func TestAConsoleOfOneModeNeverHearsTheOther(t *testing.T) {
	for _, p := range principals(t) {
		if p.name != "console A live" {
			continue
		}
		for _, s := range p.principal.Scopes {
			if s == TenantScope(orgA, false) || s == SpaceScope(acctA+"#test") {
				t.Fatalf("a live connection was granted %s", s)
			}
		}
	}
}

func TestAnAuthorizerRefusesWhatItCannotVerify(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for name, run := range map[string]func() (*Principal, error){
		"console: bad token": func() (*Principal, error) {
			return f.console.Authorize(ctx, &wire.Auth{Token: "nope", Space: "org:" + acctA})
		},
		"console: a service token": func() (*Principal, error) {
			return f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["m2m"], Space: "org:" + acctA})
		},
		"console: not a member": func() (*Principal, error) {
			return f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s1"], Space: "org:" + acctB})
		},
		"console: a malformed selector": func() (*Principal, error) {
			return f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s1"], Space: "org:../x"})
		},
		"console: no space": func() (*Principal, error) { return f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s1"]}) },
		"console: an unknown mode": func() (*Principal, error) {
			return f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s1"], Space: "org:" + acctA, Mode: "prod"})
		},
		"portal: bad token":       func() (*Principal, error) { return f.portal.Authorize(ctx, &wire.Auth{Token: "nope"}) },
		"portal: a service token": func() (*Principal, error) { return f.portal.Authorize(ctx, &wire.Auth{Token: f.tokens["m2m"]}) },
		"checkout: a forged link": func() (*Principal, error) { return f.checkout.Authorize(ctx, &wire.Auth{PayLink: "e30.AAAA"}) },
		"checkout: a link with a bad mac": func() (*Principal, error) {
			return f.checkout.Authorize(ctx, &wire.Auth{PayLink: f.links.Sign(orgA, true, "in_1") + "x"})
		},
		"checkout: an empty link": func() (*Principal, error) { return f.checkout.Authorize(ctx, &wire.Auth{}) },
	} {
		if p, err := run(); !errors.Is(err, ErrUnauthorized) || p != nil {
			t.Errorf("%s: principal=%v err=%v, want ErrUnauthorized", name, p, err)
		}
	}
}

// A viewer reads Finanças but is not a merchant: they get the space scope and
// never the tenant's.
func TestAViewerHearsTheSpaceButNotTheTenant(t *testing.T) {
	f := newFixture(t)
	p, err := f.console.Authorize(context.Background(), &wire.Auth{Token: f.tokens["viewer"], Space: "org:" + acctA, Mode: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(p.Scopes, TenantScope(orgA, true)) || !slices.Contains(p.Scopes, SpaceScope(acctA+"#live")) {
		t.Fatalf("scopes = %v", p.Scopes)
	}
}

func TestAPersonalSpaceIsTheCallersOwnAndNoOneElses(t *testing.T) {
	f := newFixture(t)
	p, err := f.console.Authorize(context.Background(), &wire.Auth{Token: f.tokens["s1"], Space: "personal", Mode: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if want := SpaceScope("USER#s1#live"); !slices.Equal(p.Scopes, []string{want}) {
		t.Fatalf("scopes = %v, want [%s]", p.Scopes, want)
	}
}

func TestTheExpiryComesFromTheTokenAndTheCheckoutGetsAFixedLifetime(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, err := f.portal.Authorize(ctx, &wire.Auth{Token: f.tokens["u1"]})
	if err != nil || !p.ExpiresAt.Equal(testNow.Add(10*time.Minute)) {
		t.Fatalf("portal expiry = %v (%v)", p.ExpiresAt, err)
	}
	p, err = f.checkout.Authorize(ctx, &wire.Auth{PayLink: f.links.Sign(orgA, true, "in_1")})
	if err != nil || !p.ExpiresAt.Equal(testNow.Add(35*time.Minute)) {
		t.Fatalf("checkout expiry = %v (%v)", p.ExpiresAt, err)
	}
}

func TestRecheckFollowsMembership(t *testing.T) {
	f := newFixture(t)
	members := fakeMembers{acctA + "|s1": {Member: true, Role: "admin", Kind: "organization"}}
	f.console.Spaces = space.NewResolver(members, nil)
	ctx := context.Background()
	p, err := f.console.Authorize(ctx, &wire.Auth{Token: f.tokens["s1"], Space: "org:" + acctA, Mode: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.console.Recheck(ctx, p); err != nil || !ok {
		t.Fatalf("a current member: %v %v", ok, err)
	}
	delete(members, acctA+"|s1")
	if ok, err := f.console.Recheck(ctx, p); err != nil || ok {
		t.Fatalf("a removed member still passes: %v %v", ok, err)
	}
}

func TestSelectPicksTheAuthorizerFromWhatTheFrameCarries(t *testing.T) {
	for _, c := range []struct {
		auth *wire.Auth
		want string
	}{
		{&wire.Auth{PayLink: "x"}, KindCheckout},
		{&wire.Auth{PayLink: "x", Token: "t", Space: "personal"}, KindCheckout}, // a link wins: it is the narrowest
		{&wire.Auth{Token: "t", Space: "org:" + acctA}, KindConsole},
		{&wire.Auth{Token: "t", Space: "personal"}, KindConsole},
		{&wire.Auth{Token: "t"}, KindPortal},
		{&wire.Auth{}, ""},
	} {
		if got := Select(c.auth); got != c.want {
			t.Errorf("Select(%+v) = %q, want %q", c.auth, got, c.want)
		}
	}
}

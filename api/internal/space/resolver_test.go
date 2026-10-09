package space

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
)

const orgB = "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff"

// fakeSource counts calls, so "zero reads" is an assertion rather than a hope.
type fakeSource struct {
	mu    sync.Mutex
	calls int
	// answers is keyed "org|user".
	answers map[string]answer
	err     error
}

type answer struct {
	role   string
	member bool
	kind   string // raw, as ctech-account sends it; "" is an organization
}

func (f *fakeSource) Membership(_ context.Context, org, user string) (string, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return "", "", false, f.err
	}
	a := f.answers[org+"|"+user]
	return a.kind, a.role, a.member, nil
}

func newResolver(src MembershipSource) (*Resolver, cache.Backend) {
	c := cache.NewMemoryBackend(100)
	return NewResolver(src, c), c
}

func resolveOrg(r *Resolver, sub, org string) (ResolvedSpace, error) {
	return r.Resolve(context.Background(), sub, Selector{OrganizationID: org}, true)
}

func TestPersonalResolvesToTheTokensOwnSubAndAsksNobody(t *testing.T) {
	src := &fakeSource{}
	r, _ := newResolver(src)
	s, err := r.Resolve(context.Background(), "alice", Selector{Personal: true}, true)
	if err != nil || s.PK() != "USER#alice#live" || !s.Personal() || !s.Can(All) {
		t.Fatalf("got %+v, %v", s, err)
	}
	if src.calls != 0 {
		t.Fatalf("membership asked %d times for a personal space", src.calls)
	}
}

func TestPersonalSelectorCannotNameAnotherUser(t *testing.T) {
	// The only way to say "personal" is the Selector{Personal:true} value, which
	// has no id field; ParseSelector refuses everything that tries to add one.
	if _, err := ParseSelector("personal:bob"); !errors.Is(err, ErrBadSelector) {
		t.Fatalf("err = %v", err)
	}
	r, _ := newResolver(&fakeSource{})
	s, _ := r.Resolve(context.Background(), "alice", Selector{Personal: true, OrganizationID: orgA}, true)
	if s.PK() != "USER#alice#live" {
		t.Fatalf("a personal selector resolved to %q", s.PK())
	}
}

func TestPersonalRefusesASubjectThatCouldBeAKey(t *testing.T) {
	r, _ := newResolver(&fakeSource{})
	for _, sub := range []string{"", "a#b", "USER#x"} {
		if _, err := r.Resolve(context.Background(), sub, Selector{Personal: true}, true); !errors.Is(err, ErrInvalidSubject) {
			t.Errorf("sub %q: err = %v, want ErrInvalidSubject", sub, err)
		}
	}
}

func TestResolveGrantsTheRolesVerbs(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{
		orgA + "|owner":  {role: "owner", member: true},
		orgA + "|member": {role: "member", member: true},
		orgA + "|viewer": {role: "viewer", member: true},
	}}
	r, _ := newResolver(src)
	for sub, want := range map[string]Verbs{"owner": All, "member": All &^ Configure, "viewer": Read} {
		s, err := resolveOrg(r, sub, orgA)
		if err != nil || s.Verbs() != want || s.PK() != orgA+"#live" || s.OrganizationID() != orgA {
			t.Errorf("%s: %+v, %v", sub, s, err)
		}
	}
}

func TestUserAWithOrganizationBIsNotFound(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{orgB + "|bob": {role: "owner", member: true}}}
	r, _ := newResolver(src)
	s, err := resolveOrg(r, "alice", orgB)
	if !errors.Is(err, ErrSpaceNotFound) || !s.IsZero() {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestAnUnknownOrganizationIsTheSameError(t *testing.T) {
	r, _ := newResolver(&fakeSource{})
	_, err := resolveOrg(r, "alice", orgB) // account answers member=false
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestAMemberWithAnUnknownRoleGetsNothing(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {role: "auditor", member: true}}}
	r, _ := newResolver(src)
	if _, err := resolveOrg(r, "alice", orgA); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("err = %v, want ErrSpaceNotFound (no verbs is no access)", err)
	}
}

// Review Focus 1.
func TestResolveRefusesAKeyShapedOrganizationID(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{"USER#alice|mallory": {role: "owner", member: true}}}
	r, _ := newResolver(src)
	for _, h := range []string{"org:USER#alice", "org:" + orgA + "#live", "org:" + orgA + "\n"} {
		sel, err := ParseSelector(h)
		if !errors.Is(err, ErrSpaceNotFound) {
			t.Errorf("%q parsed as %+v, %v", h, sel, err)
		}
	}
	// Even if a caller bypassed the parser and built the Selector by hand, the
	// resolver validates the id again before any key or call exists.
	if _, err := resolveOrg(r, "mallory", "USER#alice"); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("resolver accepted a key-shaped id: %v", err)
	}
	if src.calls != 0 {
		t.Fatalf("membership asked %d times for a malformed id", src.calls)
	}
}

func TestMembershipIsCachedBothWays(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {role: "admin", member: true}}}
	r, _ := newResolver(src)
	for i := 0; i < 3; i++ {
		if _, err := resolveOrg(r, "alice", orgA); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveOrg(r, "stranger", orgA); !errors.Is(err, ErrSpaceNotFound) {
			t.Fatal(err)
		}
	}
	if src.calls != 2 {
		t.Fatalf("source called %d times, want 2 (one positive, one negative)", src.calls)
	}
}

// Review Focus 2.
func TestMembershipCacheIsKeyedByOrganizationAndUser(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {role: "owner", member: true}}}
	r, _ := newResolver(src)
	if _, err := resolveOrg(r, "alice", orgA); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveOrg(r, "bob", orgA); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("bob inherited alice's cached grant: %v", err)
	}
	if _, err := resolveOrg(r, "alice", orgB); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("alice inherited her grant on another organization: %v", err)
	}
}

func TestARemovedMemberLosesAccessWhenTheCacheExpires(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {role: "owner", member: true}}}
	r, c := newResolver(src)
	if _, err := resolveOrg(r, "alice", orgA); err != nil {
		t.Fatal(err)
	}
	delete(src.answers, orgA+"|alice") // removed in ctech-account
	if _, err := resolveOrg(r, "alice", orgA); err != nil {
		t.Fatalf("within the TTL the cached answer stands (accepted limit): %v", err)
	}
	if err := c.DeletePrefix(context.Background(), "billing:space:"); err != nil { // the TTL elapsing
		t.Fatal(err)
	}
	if _, err := resolveOrg(r, "alice", orgA); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("after expiry the removed member still resolves: %v", err)
	}
}

func TestAccountUnreachableFailsClosed(t *testing.T) {
	src := &fakeSource{err: errors.New("dial tcp: i/o timeout")}
	r, _ := newResolver(src)
	_, err := resolveOrg(r, "alice", orgA)
	if !errors.Is(err, ErrSpaceUnavailable) || errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("err = %v, want ErrSpaceUnavailable and not ErrSpaceNotFound", err)
	}
}

// Review Focus 3.
func TestAnOutageIsNeverCached(t *testing.T) {
	src := &fakeSource{err: errors.New("503"), answers: map[string]answer{orgA + "|alice": {role: "owner", member: true}}}
	r, _ := newResolver(src)
	if _, err := resolveOrg(r, "alice", orgA); !errors.Is(err, ErrSpaceUnavailable) {
		t.Fatal(err)
	}
	src.err = nil // account recovers
	if _, err := resolveOrg(r, "alice", orgA); err != nil {
		t.Fatalf("a blip became a cached refusal: %v", err)
	}
}

func TestANilSourceFailsClosed(t *testing.T) {
	r := NewResolver(nil, cache.NewMemoryBackend(10))
	if _, err := resolveOrg(r, "alice", orgA); !errors.Is(err, ErrSpaceUnavailable) {
		t.Fatalf("err = %v: an unconfigured membership source must read as a refusal, never as permission", err)
	}
	// Personal spaces need no account and still work.
	if _, err := r.Resolve(context.Background(), "alice", Selector{Personal: true}, false); err != nil {
		t.Fatal(err)
	}
}

func TestModeIsPartOfTheSpace(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {role: "owner", member: true}}}
	r, _ := newResolver(src)
	live, _ := r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgA}, true)
	test, _ := r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgA}, false)
	if live.PK() == test.PK() || test.PK() != orgA+"#test" {
		t.Fatalf("live %q test %q", live.PK(), test.PK())
	}
}

const wsA = "0190a1b2-c3d4-7e5f-8a9b-aaaaaaaaaaaa" // a personal workspace
const wsB = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"

func TestAPersonalWorkspaceGrantsByItsOwnTable(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{
		wsA + "|owner":  {kind: "personal", role: "owner", member: true},
		wsA + "|member": {kind: "personal", role: "member", member: true},
		wsA + "|viewer": {kind: "personal", role: "viewer", member: true},
	}}
	r, _ := newResolver(src)
	for sub, want := range map[string]Verbs{"owner": All, "member": All, "viewer": Read} {
		s, err := resolveOrg(r, sub, wsA)
		if err != nil || s.Verbs() != want || s.Kind() != KindPersonal || s.PK() != wsA+"#live" || s.Personal() {
			t.Errorf("%s: %+v, %v", sub, s, err)
		}
	}
}

// § 9.3
func TestAdminOnAPersonalWorkspaceOrAnUnknownKindIsNotFound(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{
		wsA + "|alice": {kind: "personal", role: "admin", member: true},
		wsB + "|alice": {kind: "team", role: "owner", member: true},
	}}
	r, _ := newResolver(src)
	for _, ws := range []string{wsA, wsB} {
		s, err := resolveOrg(r, "alice", ws)
		if !errors.Is(err, ErrSpaceNotFound) || !s.IsZero() {
			t.Errorf("%s: %+v, %v; want the ordinary 404", ws, s, err)
		}
	}
}

// § 9.4
func TestARemovedPersonalWorkspaceMemberLosesAccessWhenTheCacheExpires(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{wsA + "|alice": {kind: "personal", role: "member", member: true}}}
	r, c := newResolver(src)
	if _, err := resolveOrg(r, "alice", wsA); err != nil {
		t.Fatal(err)
	}
	delete(src.answers, wsA+"|alice") // removed in ctech-account
	if _, err := resolveOrg(r, "alice", wsA); err != nil {
		t.Fatalf("within the TTL the cached answer stands (accepted limit): %v", err)
	}
	if err := c.DeletePrefix(context.Background(), "billing:space:"); err != nil { // the TTL elapsing
		t.Fatal(err)
	}
	if _, err := resolveOrg(r, "alice", wsA); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("after expiry the removed member still resolves: %v", err)
	}
}

// § 9.4
func TestAnOutageIsNeverCachedForAPersonalWorkspace(t *testing.T) {
	src := &fakeSource{err: errors.New("503"), answers: map[string]answer{wsA + "|alice": {kind: "personal", role: "viewer", member: true}}}
	r, _ := newResolver(src)
	if _, err := resolveOrg(r, "alice", wsA); !errors.Is(err, ErrSpaceUnavailable) {
		t.Fatal(err)
	}
	src.err = nil
	s, err := resolveOrg(r, "alice", wsA)
	if err != nil || s.Verbs() != Read {
		t.Fatalf("a blip became a cached refusal: %+v, %v", s, err)
	}
}

// § 9.8: an entry written before this deploy has no kind. It must read as an
// organization — for member that is FEWER verbs than personal, never more.
func TestACachedEntryWithoutKindGrantsTheOrganizationVerbs(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{wsA + "|alice": {kind: "personal", role: "member", member: true}}}
	r, c := newResolver(src)
	if err := c.Set(context.Background(), cacheKey(wsA, "alice"), []byte(`{"member":true,"role":"member"}`), MembershipTTLSeconds); err != nil {
		t.Fatal(err)
	}
	s, err := resolveOrg(r, "alice", wsA)
	if err != nil || s.Verbs() != All&^Configure || s.Kind() != KindOrganization {
		t.Fatalf("got %+v (verbs %v), %v; want the organization member's verbs", s, s.Verbs(), err)
	}
	if src.calls != 0 {
		t.Fatalf("the cached entry was not used: %d call(s)", src.calls)
	}
}

// Deploy order: ctech-account not sending kind yet reads as organization.
func TestAMissingKindIsAnOrganization(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {role: "member", member: true}}}
	r, _ := newResolver(src)
	s, err := resolveOrg(r, "alice", orgA)
	if err != nil || s.Kind() != KindOrganization || s.Verbs() != All&^Configure {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestTheCacheStoresTheKind(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{wsA + "|alice": {kind: "personal", role: "member", member: true}}}
	r, c := newResolver(src)
	if _, err := resolveOrg(r, "alice", wsA); err != nil {
		t.Fatal(err)
	}
	raw, ok, err := c.Get(context.Background(), cacheKey(wsA, "alice"))
	if err != nil || !ok || !strings.Contains(string(raw), `"kind":"personal"`) {
		t.Fatalf("cache entry = %s, %v, %v", raw, ok, err)
	}
	s, _ := resolveOrg(r, "alice", wsA) // from the cache
	if s.Verbs() != All || src.calls != 1 {
		t.Fatalf("cached read: verbs %v, calls %d", s.Verbs(), src.calls)
	}
}

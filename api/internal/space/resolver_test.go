package space

import (
	"context"
	"errors"
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
}

func (f *fakeSource) Membership(_ context.Context, org, user string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return "", false, f.err
	}
	a := f.answers[org+"|"+user]
	return a.role, a.member, nil
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
		orgA + "|owner":  {"owner", true},
		orgA + "|member": {"member", true},
		orgA + "|viewer": {"viewer", true},
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
	src := &fakeSource{answers: map[string]answer{orgB + "|bob": {"owner", true}}}
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
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {"auditor", true}}}
	r, _ := newResolver(src)
	if _, err := resolveOrg(r, "alice", orgA); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("err = %v, want ErrSpaceNotFound (no verbs is no access)", err)
	}
}

// Review Focus 1.
func TestResolveRefusesAKeyShapedOrganizationID(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{"USER#alice|mallory": {"owner", true}}}
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
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {"admin", true}}}
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
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {"owner", true}}}
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
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {"owner", true}}}
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
	src := &fakeSource{err: errors.New("503"), answers: map[string]answer{orgA + "|alice": {"owner", true}}}
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
	src := &fakeSource{answers: map[string]answer{orgA + "|alice": {"owner", true}}}
	r, _ := newResolver(src)
	live, _ := r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgA}, true)
	test, _ := r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgA}, false)
	if live.PK() == test.PK() || test.PK() != orgA+"#test" {
		t.Fatalf("live %q test %q", live.PK(), test.PK())
	}
}

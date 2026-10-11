package space

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
)

const orgA = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func TestVerbsFor(t *testing.T) {
	cases := []struct {
		kind Kind
		role string
		want Verbs
	}{
		// organization: unchanged from 6.2
		{KindOrganization, "owner", All},
		{KindOrganization, "admin", All},
		{KindOrganization, "member", All &^ Configure},
		{KindOrganization, "viewer", Read},
		{KindOrganization, "", 0},
		{KindOrganization, "superuser", 0},
		{KindOrganization, "OWNER", 0}, // roles are exact
		// personal workspace (ADR 0027): Dono, Acesso total, Leitura
		{KindPersonal, "owner", All},
		{KindPersonal, "member", All},
		{KindPersonal, "viewer", Read},
		{KindPersonal, "admin", 0}, // refused upstream on this kind: seeing it means something is wrong
		{KindPersonal, "superuser", 0},
		// a kind billing does not know grants nothing, whatever the role
		{Kind("team"), "owner", 0},
		{Kind(""), "owner", 0},
		{KindPersonalDefault, "owner", 0}, // never reached through membership
	}
	for _, c := range cases {
		if got := VerbsFor(c.kind, c.role); got != c.want {
			t.Errorf("VerbsFor(%q, %q) = %v, want %v", c.kind, c.role, got, c.want)
		}
	}
}

func TestWorkspaceKind(t *testing.T) {
	for raw, want := range map[string]Kind{
		"":             KindOrganization, // a route that predates kinds
		"organization": KindOrganization,
		"personal":     KindPersonal,
		"team":         Kind("team"), // kept, so VerbsFor refuses it
	} {
		if got := WorkspaceKind(raw); got != want {
			t.Errorf("WorkspaceKind(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestKindOfASpace(t *testing.T) {
	p, _ := personalSpace("alice", true)
	if p.Kind() != KindPersonalDefault {
		t.Errorf("personal default kind = %q", p.Kind())
	}
	if k := orgSpace(orgA, KindPersonal, true, Read).Kind(); k != KindPersonal {
		t.Errorf("workspace kind = %q", k)
	}
	if (ResolvedSpace{}).Kind() != "" {
		t.Error("the zero space has a kind")
	}
}

func TestVerbNames(t *testing.T) {
	got := strings.Join((Read | Settle).Names(), ",")
	if got != "finance.read,finance.settle" {
		t.Fatalf("Names = %s", got)
	}
}

func TestParseSelector(t *testing.T) {
	ok := map[string]Selector{
		"personal":    {Personal: true},
		"org:" + orgA: {OrganizationID: orgA},
	}
	for h, want := range ok {
		got, err := ParseSelector(h)
		if err != nil || got != want {
			t.Errorf("ParseSelector(%q) = %+v, %v; want %+v", h, got, err, want)
		}
	}
	for _, h := range []string{"", "Personal", "personal:" + orgA, "personal ", "org", "org:", "tenant:" + orgA} {
		if _, err := ParseSelector(h); !errors.Is(err, ErrBadSelector) {
			t.Errorf("ParseSelector(%q) err = %v, want ErrBadSelector", h, err)
		}
	}
	// A well-formed selector with a malformed organization id is "not found",
	// the same answer as an id that does not exist.
	for _, id := range []string{
		"USER#abc", orgA + "#live", strings.ToUpper(orgA), orgA[:35], orgA + "x", "../" + orgA, " " + orgA,
	} {
		if _, err := ParseSelector("org:" + id); !errors.Is(err, ErrSpaceNotFound) {
			t.Errorf("ParseSelector(org:%q) err = %v, want ErrSpaceNotFound", id, err)
		}
	}
}

func TestForJob(t *testing.T) {
	s, err := ForJob(orgA, true)
	if err != nil || s.PK() != orgA+"#live" || s.Personal() || !s.Can(All) {
		t.Fatalf("org: %+v, %v", s, err)
	}
	s, err = ForJob("USER#u1", false)
	if err != nil || s.PK() != "USER#u1#test" || !s.Personal() || s.OrganizationID() != "" {
		t.Fatalf("personal: %+v, %v", s, err)
	}
	for _, bad := range []string{"", "USER#", "USER#a#b", "not-a-uuid", orgA + "#live"} {
		if _, err := ForJob(bad, true); err == nil {
			t.Errorf("ForJob(%q) was accepted", bad)
		}
	}
}

func TestZeroSpaceIsRefused(t *testing.T) {
	var z ResolvedSpace
	if !z.IsZero() || z.PK() != "" || z.Can(Read) {
		t.Fatal("the zero value must carry no key and no verb")
	}
	if err := z.Require(Read); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Require on zero = %v, want ErrNoSpace", err)
	}
	s, _ := ForJob(orgA, true)
	viewer := Narrow(s, Read)
	if err := viewer.Require(Write); !errors.Is(err, ErrDenied) {
		t.Fatalf("Require(Write) as viewer = %v, want ErrDenied", err)
	}
}

// ResolvedSpace is non-forgeable because no code outside this package can set
// its fields. Pin that: if someone exports a field, a handler can fill it from
// a header and every guarantee in ADR 0025 is gone.
func TestResolvedSpaceHasNoExportedFields(t *testing.T) {
	rt := reflect.TypeOf(ResolvedSpace{})
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).IsExported() {
			t.Fatalf("ResolvedSpace.%s is exported", rt.Field(i).Name)
		}
	}
}

func TestAnOrganizationSpaceCarriesTheRoleCtechAccountSent(t *testing.T) {
	src := &fakeSource{answers: map[string]answer{
		orgB + "|alice": {member: true, role: "admin", kind: "organization"},
	}}
	r := NewResolver(src, cache.NewMemoryBackend(10))
	sp, err := r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgB}, true)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Role() != "admin" {
		t.Fatalf("role = %q, want admin", sp.Role())
	}
	// From the cache too: the second answer must not lose the role.
	sp, _ = r.Resolve(context.Background(), "alice", Selector{OrganizationID: orgB}, true)
	if sp.Role() != "admin" || src.calls != 1 {
		t.Fatalf("cached role = %q, calls = %d", sp.Role(), src.calls)
	}
}

func TestThePersonalDefaultSpaceAndJobSpacesHaveNoRole(t *testing.T) {
	r := NewResolver(nil, nil)
	sp, err := r.Resolve(context.Background(), "alice", Selector{Personal: true}, false)
	if err != nil || sp.Role() != "" {
		t.Fatalf("personal role = %q, %v", sp.Role(), err)
	}
	job, err := ForJob(orgB, true)
	if err != nil || job.Role() != "" {
		t.Fatalf("job role = %q, %v", job.Role(), err)
	}
}

func TestNarrowKeepsTheRole(t *testing.T) {
	sp := orgSpace(orgB, KindOrganization, true, All).withRole("owner")
	if Narrow(sp, Read).Role() != "owner" {
		t.Fatal("Narrow dropped the role")
	}
}

func TestOnlyOwnersAndAdminsOfOrganizationsAreMerchants(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		role string
		want bool
	}{
		{KindOrganization, "owner", true},
		{KindOrganization, "admin", true},
		{KindOrganization, "member", false},
		{KindOrganization, "viewer", false},
		{KindOrganization, "", false},
		{KindOrganization, "Owner", false}, // roles are lower-case on the wire; no folding
		{KindPersonal, "owner", false},
		{KindPersonalDefault, "owner", false},
		{Kind("team"), "owner", false},
	} {
		if got := IsMerchantRole(c.kind, c.role); got != c.want {
			t.Errorf("IsMerchantRole(%q, %q) = %v, want %v", c.kind, c.role, got, c.want)
		}
	}
}

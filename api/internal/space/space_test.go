package space

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const orgA = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func TestVerbsForRole(t *testing.T) {
	cases := []struct {
		role string
		want Verbs
	}{
		{"owner", All},
		{"admin", All},
		{"member", All &^ Configure},
		{"viewer", Read},
		{"", 0},
		{"superuser", 0},
		{"OWNER", 0}, // roles are exact: an unknown spelling grants nothing
	}
	for _, c := range cases {
		if got := VerbsForRole(c.role); got != c.want {
			t.Errorf("VerbsForRole(%q) = %v, want %v", c.role, got, c.want)
		}
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

// ForJob exists for binaries (the daily job, the rebuild command), which choose
// a space by operator decision. A handler or middleware calling it would build
// a space from request data, which is the thing this package prevents.
func TestForJobIsNotUsedOnTheRequestPath(t *testing.T) {
	for _, dir := range []string{"../api", "../middleware", "../services"} {
		err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if strings.Contains(string(b), "space.ForJob") {
				t.Errorf("%s calls space.ForJob; it is for cmd/ binaries only", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

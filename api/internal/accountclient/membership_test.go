package accountclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body string) (*Client, *string) {
	t.Helper()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.RequestURI
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), baseURL: srv.URL}, &gotPath
}

func TestMembershipReadsRoleAndPath(t *testing.T) {
	c, path := serve(t, 200, `{"member":true,"role":"admin"}`)
	_, role, member, err := c.membershipWithToken(context.Background(), "tok", "org-1", "user-1")
	if err != nil || !member || role != "admin" {
		t.Fatalf("got %q %v %v", role, member, err)
	}
	if *path != "/v1.0/internal/organizations/org-1/members/user-1" {
		t.Fatalf("path = %s", *path)
	}
}

func TestANonMemberIsAnAnswerNotAnError(t *testing.T) {
	c, _ := serve(t, 200, `{"member":false}`)
	_, role, member, err := c.membershipWithToken(context.Background(), "tok", "o", "u")
	if err != nil || member || role != "" {
		t.Fatalf("got %q %v %v", role, member, err)
	}
}

func TestEveryNon200IsAnError(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500, 503} {
		c, _ := serve(t, status, `{"member":true,"role":"owner"}`)
		if _, _, member, err := c.membershipWithToken(context.Background(), "tok", "o", "u"); err == nil || member {
			t.Errorf("status %d: member=%v err=%v; a non-200 must be an error and never a grant", status, member, err)
		}
	}
}

func TestMalformedBodyIsAnError(t *testing.T) {
	c, _ := serve(t, 200, `<html>`)
	if _, _, member, err := c.membershipWithToken(context.Background(), "tok", "o", "u"); err == nil || member {
		t.Fatalf("member=%v err=%v", member, err)
	}
}

func TestIdsAreEscapedIntoThePath(t *testing.T) {
	c, path := serve(t, 200, `{"member":false}`)
	_, _, _, _ = c.membershipWithToken(context.Background(), "tok", "a/../b", "u?x=1")
	if want := "/v1.0/internal/organizations/a%2F..%2Fb/members/u%3Fx=1"; *path != want {
		t.Fatalf("request URI = %q, want %q", *path, want)
	}
}

func TestANilClientRefuses(t *testing.T) {
	var c *Client
	if _, _, member, err := c.Membership(context.Background(), "o", "u"); err == nil || member {
		t.Fatalf("a nil client must refuse: member=%v err=%v", member, err)
	}
}

func TestNewNeedsTheWholeCredential(t *testing.T) {
	if New(Config{BaseURL: "https://x"}) != nil {
		t.Fatal("an incomplete credential produced a client")
	}
	if New(Config{BaseURL: "https://x", TokenURL: "https://x/t", ClientID: "i", ClientSecret: "s"}) == nil {
		t.Fatal("a complete credential produced no client")
	}
}

func TestOrganizationsReadsTheListAndEscapesTheUser(t *testing.T) {
	c, path := serve(t, 200, `{"organizations":[{"id":"o1","display_name":"Acme","role":"admin"},{"id":"o2","display_name":"Beta","role":"viewer"}]}`)
	got, err := c.organizationsWithToken(context.Background(), "tok", "u?1")
	if err != nil || len(got) != 2 || got[0].Role != "admin" || got[1].DisplayName != "Beta" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if want := "/v1.0/internal/users/u%3F1/organizations"; *path != want {
		t.Fatalf("request URI = %q, want %q", *path, want)
	}
}

func TestOrganizationsFailsClosed(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500, 503} {
		c, _ := serve(t, status, `{"organizations":[]}`)
		if _, err := c.organizationsWithToken(context.Background(), "tok", "u"); err == nil {
			t.Errorf("status %d was read as an empty list", status)
		}
	}
	var nilClient *Client
	if _, err := nilClient.Organizations(context.Background(), "u"); err == nil {
		t.Error("a nil client answered")
	}
}

// Listing a user's organizations is its own scope at ctech-account, and it is
// fetched with its own token: if that scope has not been granted yet, the
// membership checks — which authorize every org-space request — must keep
// working on theirs.
func TestEachQuestionMintsItsOwnScope(t *testing.T) {
	scopes := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			scopes[r.Form.Get("scope")] = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/members/") {
			_, _ = w.Write([]byte(`{"member":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"organizations":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := New(Config{BaseURL: srv.URL, TokenURL: srv.URL + "/token", ClientID: "billing", ClientSecret: "s"})
	if _, _, _, err := c.Membership(context.Background(), "o", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Organizations(context.Background(), "u"); err != nil {
		t.Fatal(err)
	}
	if !scopes[Scope] || !scopes[ListScope] || len(scopes) != 2 {
		t.Fatalf("token scopes requested = %v, want exactly %q and %q, separately", scopes, Scope, ListScope)
	}
}

func TestMembershipCarriesTheKind(t *testing.T) {
	for body, want := range map[string][3]string{
		`{"member":true,"role":"member","kind":"personal"}`: {"personal", "member", "true"},
		`{"member":true,"role":"owner"}`:                    {"", "owner", "true"}, // before ctech-account sends kind
		`{"member":false}`:                                  {"", "", "false"},
		`{"member":false,"kind":"personal","role":"owner"}`: {"", "", "false"}, // a refusal carries nothing
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		c := &Client{http: srv.Client(), baseURL: srv.URL}
		kind, role, member, err := c.membershipWithToken(context.Background(), "tok", "o", "u")
		srv.Close()
		if err != nil || kind != want[0] || role != want[1] || fmt.Sprint(member) != want[2] {
			t.Errorf("%s: kind %q role %q member %v err %v", body, kind, role, member, err)
		}
	}
}

func TestOrganizationsCarryTheKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"organizations":[{"id":"a","display_name":"Casa","role":"owner","kind":"personal"},{"id":"b","display_name":"Acme","role":"admin","kind":"organization"}]}`))
	}))
	defer srv.Close()
	c := &Client{http: srv.Client(), baseURL: srv.URL}
	got, err := c.organizationsWithToken(context.Background(), "tok", "u")
	if err != nil || len(got) != 2 || got[0].Kind != "personal" || got[1].Kind != "organization" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

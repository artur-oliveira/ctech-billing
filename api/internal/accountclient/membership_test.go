package accountclient

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	role, member, err := c.membershipWithToken(context.Background(), "tok", "org-1", "user-1")
	if err != nil || !member || role != "admin" {
		t.Fatalf("got %q %v %v", role, member, err)
	}
	if *path != "/v1.0/internal/organizations/org-1/members/user-1" {
		t.Fatalf("path = %s", *path)
	}
}

func TestANonMemberIsAnAnswerNotAnError(t *testing.T) {
	c, _ := serve(t, 200, `{"member":false}`)
	role, member, err := c.membershipWithToken(context.Background(), "tok", "o", "u")
	if err != nil || member || role != "" {
		t.Fatalf("got %q %v %v", role, member, err)
	}
}

func TestEveryNon200IsAnError(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500, 503} {
		c, _ := serve(t, status, `{"member":true,"role":"owner"}`)
		if _, member, err := c.membershipWithToken(context.Background(), "tok", "o", "u"); err == nil || member {
			t.Errorf("status %d: member=%v err=%v; a non-200 must be an error and never a grant", status, member, err)
		}
	}
}

func TestMalformedBodyIsAnError(t *testing.T) {
	c, _ := serve(t, 200, `<html>`)
	if _, member, err := c.membershipWithToken(context.Background(), "tok", "o", "u"); err == nil || member {
		t.Fatalf("member=%v err=%v", member, err)
	}
}

func TestIdsAreEscapedIntoThePath(t *testing.T) {
	c, path := serve(t, 200, `{"member":false}`)
	_, _, _ = c.membershipWithToken(context.Background(), "tok", "a/../b", "u?x=1")
	if want := "/v1.0/internal/organizations/a%2F..%2Fb/members/u%3Fx=1"; *path != want {
		t.Fatalf("request URI = %q, want %q", *path, want)
	}
}

func TestANilClientRefuses(t *testing.T) {
	var c *Client
	if _, member, err := c.Membership(context.Background(), "o", "u"); err == nil || member {
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

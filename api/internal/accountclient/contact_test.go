package accountclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
)

func TestContactReadsOnlyThePersonsContactAndMapsAnUnknownUser(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":900}`))
		case "/v1.0/internal/users/u1/contact":
			gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"email":"ana@example.com","first_name":"Ana","email_verified":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewContacts(Config{BaseURL: srv.URL, TokenURL: srv.URL + "/token", ClientID: "billing", ClientSecret: "s", Cache: cache.NewMemoryBackend(10)})

	got, err := c.Contact(context.Background(), "u1")
	if err != nil || got.Email != "ana@example.com" || got.FirstName != "Ana" || !got.EmailVerified || gotPath == "" || gotAuth != "Bearer tok" {
		t.Fatalf("contact = %+v, %v (path %q auth %q)", got, err, gotPath, gotAuth)
	}
	if _, err := c.Contact(context.Background(), "nobody"); err != ErrContactNotFound {
		t.Fatalf("an unknown user: %v, want ErrContactNotFound", err)
	}
	var none *Contacts
	if _, err := none.Contact(context.Background(), "u1"); err == nil {
		t.Fatal("a nil client must error, not answer")
	}
}

package accountclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gopkg.aoctech.app/api-commons/resilient"
)

const org = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), baseURL: srv.URL}
}

func TestCompaniesListsTheOrganizationsCompanies(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/internal/organizations/"+org+"/companies" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"organization","companies":[{"company_id":"cmp_1","legal_name":"Padaria LTDA","trade_name":"Bom Dia","tax_id":"12345678000190","tax_id_kind":"cnpj"}],"truncated":false}`))
	})
	got, err := c.companiesWithToken(context.Background(), "tok", org)
	if err != nil || len(got.Companies) != 1 || got.Companies[0].ID != "cmp_1" || got.Companies[0].LegalName != "Padaria LTDA" ||
		got.Companies[0].TaxID != "12345678000190" || got.Truncated {
		t.Fatalf("companies = %+v, %v", got, err)
	}
}

// ctech-account caps the list at 200 by legal name and says so; the caller
// must be able to tell a cut list from the whole one.
func TestATruncatedListSaysSo(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"organization","companies":[{"company_id":"cmp_1","legal_name":"A"}],"truncated":true}`))
	})
	got, err := c.companiesWithToken(context.Background(), "tok", org)
	if err != nil || !got.Truncated {
		t.Fatalf("list = %+v, %v; want truncated", got, err)
	}
}

func TestAnEmptyListIsAnEmptySliceNotNil(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"organization","companies":[],"truncated":false}`))
	})
	got, err := c.companiesWithToken(context.Background(), "tok", org)
	if err != nil || got.Companies == nil || len(got.Companies) != 0 {
		t.Fatalf("list = %+v, %v", got, err)
	}
}

// ctech-account answers 404 for an organization it does not know: that is
// an answer (the organization does not exist), not an outage.
func TestAnUnknownOrganizationIsNotFoundNotAnOutage(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if _, err := c.companiesWithToken(context.Background(), "tok", org); !errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("err = %v, want ErrOrganizationNotFound", err)
	}
}

// A personal workspace is not a merchant (D6): its companies are not listed.
func TestAPersonalWorkspacesListIsNotFound(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"personal","companies":[{"company_id":"cmp_1"}],"truncated":false}`))
	})
	if _, err := c.companiesWithToken(context.Background(), "tok", org); !errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnOutageIsAnError(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusServiceUnavailable) })
	_, err := c.companiesWithToken(context.Background(), "tok", org)
	if err == nil || errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

func TestCompanyReadsTheIdentityRoute(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/internal/organizations/"+org+"/companies/cmp_1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"organization","tax_id":"12345678000190","tax_id_kind":"cnpj","legal_name":"Padaria LTDA","trade_name":"Bom Dia"}`))
	})
	got, err := c.companyWithToken(context.Background(), "tok", org, "cmp_1")
	if err != nil || got.ID != "cmp_1" || got.LegalName != "Padaria LTDA" {
		t.Fatalf("company = %+v, %v", got, err)
	}
	if _, err := c.companyWithToken(context.Background(), "tok", org, "cmp_other"); !errors.Is(err, ErrCompanyNotFound) {
		t.Fatalf("unknown company: %v", err)
	}
}

// A company in a personal workspace is not a merchant's billing company (D6).
func TestACompanyInAPersonalWorkspaceIsNotFound(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"organization_id":"` + org + `","organization_kind":"personal","tax_id":"1","legal_name":"X"}`))
	})
	if _, err := c.companyWithToken(context.Background(), "tok", org, "cmp_1"); !errors.Is(err, ErrCompanyNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestANilClientRefuses(t *testing.T) {
	var c *Client
	if _, err := c.Companies(context.Background(), org); err == nil {
		t.Fatal("nil client answered")
	}
	if New(Config{BaseURL: "x"}) != nil {
		t.Fatal("an incomplete config built a client")
	}
}

// While ctech-account is down, five failures in a row open the circuit and the next
// call is refused at once, without touching the host: a request no longer holds a
// goroutine for the whole timeout.
func TestADownAccountOpensTheCircuitAndTheNextCallIsRefusedAtOnce(t *testing.T) {
	var apiHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
			return
		}
		apiHits.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	c := New(Config{BaseURL: srv.URL, TokenURL: srv.URL + "/token", ClientID: "id", ClientSecret: "secret"})

	for i := 0; i < 5; i++ {
		if _, err := c.Companies(context.Background(), org); err == nil {
			t.Fatalf("call %d against a down account succeeded", i)
		}
	}
	hits := apiHits.Load()
	_, err := c.Companies(context.Background(), org)
	if !errors.Is(err, resilient.ErrCircuitOpen) {
		t.Fatalf("err = %v, want the open-circuit error", err)
	}
	if apiHits.Load() != hits {
		t.Fatal("the refused call still reached ctech-account")
	}
}

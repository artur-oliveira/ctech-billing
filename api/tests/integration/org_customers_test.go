//go:build integration

package integration

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func newOrgRef() (string, string) {
	org := newSpaceOrgID()
	return org, "ORG_" + org
}

func TestCreatingAnOrganizationCustomerTwiceIsOneCustomer(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	org, ref := newOrgRef()
	body := `{"name":"Acme LTDA","tax_id":"11222333000181","external_ref":"` + ref + `"}`
	first := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), body)
	second := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), body)
	if first.status != http.StatusCreated || second.status != http.StatusOK {
		t.Fatalf("first %d %s, second %d %s", first.status, first.body, second.status, second.body)
	}
	var a, b struct {
		ID string `json:"id"`
	}
	first.decode(t, &a)
	second.decode(t, &b)
	if a.ID != b.ID {
		t.Fatalf("two customers: %s, %s", a.ID, b.ID)
	}
	got, err := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctxT(t), e.org.ID, true, org)
	if err != nil || got.ID != a.ID {
		t.Fatalf("pointer = %+v, %v", got, err)
	}
}

// The pointer and the customer are one transaction: a taken pointer leaves no
// orphan customer behind.
func TestATakenPointerWritesNoCustomer(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	repo := repositories.NewCustomerRepository(testDB, testCfg)
	_, ref := newOrgRef()
	first := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: true, Name: "A", ExternalRef: ref}
	if err := repo.Create(ctx, first, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	second := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: true, Name: "B", ExternalRef: ref}
	if err := repo.Create(ctx, second, "test", "r", now()); !errors.Is(err, repositories.ErrOrganizationAlreadyCustomer) {
		t.Fatalf("second create: %v", err)
	}
	if _, err := repo.Get(ctx, org.ID, true, second.ID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("an orphan customer was written: %v", err)
	}
}

// Review Focus 4.
func TestAnOrganizationCustomerCannotCarryAUser(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	_, ref := newOrgRef()
	r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Acme","external_ref":"`+ref+`","user_id":"usr_1"}`)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// A second customer for an account that already is one was a 500.
func TestASecondCustomerForOneUserIsA409(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	user := "usr_" + id.New()
	if r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana","user_id":"`+user+`"}`); r.status != http.StatusCreated {
		t.Fatalf("first: %d %s", r.status, r.body)
	}
	r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Ana","user_id":"`+user+`"}`)
	if r.status != http.StatusConflict || !strings.Contains(string(r.body), "user_already_customer") {
		t.Fatalf("second: %d %s", r.status, r.body)
	}
}

func TestAnOrgPrefixThatIsNotAnIDIsAnOrdinaryRef(t *testing.T) {
	e := newAPI(t)
	tok := e.token(t, e.client, "", middleware.ScopeCustomersWrite)
	for i := 0; i < 2; i++ {
		if r := e.do(t, http.MethodPost, "/v1.0/customers", tok, id.New(), `{"name":"Acme","external_ref":"ORG_acme"}`); r.status != http.StatusCreated {
			t.Fatalf("%d: %d %s", i, r.status, r.body)
		}
	}
}

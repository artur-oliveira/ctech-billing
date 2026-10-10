package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type kinded struct {
	calls int
	m     map[string][2]string // "org|user" -> {kind, role}
}

func (k *kinded) Membership(_ context.Context, org, user string) (string, string, bool, error) {
	k.calls++
	a, ok := k.m[org+"|"+user]
	return a[0], a[1], ok, nil
}

type portalCustomers struct{ reads int }

func (p *portalCustomers) GetByUser(_ context.Context, _ string, _ bool, user string) (*billing.Customer, error) {
	p.reads++
	return &billing.Customer{ID: "cus_" + user}, nil
}
func (p *portalCustomers) GetByOrganization(_ context.Context, _ string, _ bool, org string) (*billing.Customer, error) {
	p.reads++
	if org == orgA {
		return &billing.Customer{ID: "cus_org"}, nil
	}
	return nil, repositories.ErrNotFound
}

func portalApp(src *kinded, cust *portalCustomers) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { c.Locals(ClaimsKey, &Claims{Sub: "alice", SID: "s"}); return c.Next() })
	app.Use(ResolvePortalIdentity(cust, "ctech", space.NewResolver(src, nil)))
	app.Get("/p", func(c fiber.Ctx) error { return c.SendString(GetCustomer(c).ID) })
	return app
}

func portalGet(t *testing.T, app *fiber.App, sel string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/p", nil)
	if sel != "" {
		req.Header.Set(SpaceHeader, sel)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 512)
	n, _ := resp.Body.Read(b)
	return resp.StatusCode, string(b[:n])
}

func TestThePortalWithoutASelectorIsThePerson(t *testing.T) {
	code, body := portalGet(t, portalApp(&kinded{}, &portalCustomers{}), "")
	if code != 200 || body != "cus_alice" {
		t.Fatalf("%d %s", code, body)
	}
}

func TestAnOwnerOrAdminOfAnOrganizationSeesItsCustomer(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		src := &kinded{m: map[string][2]string{orgA + "|alice": {"organization", role}}}
		if code, body := portalGet(t, portalApp(src, &portalCustomers{}), "org:"+orgA); code != 200 || body != "cus_org" {
			t.Fatalf("%s: %d %s", role, code, body)
		}
	}
}

// Review Focus 5 (and spec test 9): member, viewer, a personal workspace and a
// stranger all get the same 404, and nothing was read before the membership.
func TestAMemberOrViewerGetsTheSame404(t *testing.T) {
	var first string
	for name, m := range map[string]map[string][2]string{
		"member":   {orgA + "|alice": {"organization", "member"}},
		"viewer":   {orgA + "|alice": {"organization", "viewer"}},
		"personal": {orgA + "|alice": {"personal", "owner"}},
		"stranger": {},
	} {
		cust := &portalCustomers{}
		code, body := portalGet(t, portalApp(&kinded{m: m}, cust), "org:"+orgA)
		if code != 404 || cust.reads != 0 {
			t.Fatalf("%s: %d %s, %d reads", name, code, body, cust.reads)
		}
		if first == "" {
			first = body
		} else if body != first {
			t.Fatalf("%s: body differs: %s vs %s", name, body, first)
		}
	}
}

func TestAMalformedOrganizationSelectorIsTheSame404(t *testing.T) {
	cust := &portalCustomers{}
	if code, _ := portalGet(t, portalApp(&kinded{}, cust), "org:USER#bob"); code != 404 || cust.reads != 0 {
		t.Fatalf("%d, %d reads", code, cust.reads)
	}
}

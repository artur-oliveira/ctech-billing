package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/authguard"
)

// Billing's M2M routes are guarded by api-commons authguard reading the claims
// the auth middleware stores under ClaimsKey. This pins that the two agree: a
// service token with the scope passes, a session token with the same scope does
// not, and no claims is a 401.
func TestInternalScopeGuardReadsBillingClaims(t *testing.T) {
	cases := []struct {
		name   string
		claims *Claims
		want   int
	}{
		{"service token with scope", &Claims{Sub: "svc", Scope: ScopeByokNotify}, 200},
		{"session token with scope", &Claims{Sub: "u", SID: "s", Scope: ScopeByokNotify}, 403},
		{"service token without scope", &Claims{Sub: "svc", Scope: ScopeInvoicesRead}, 403},
		{"no claims", nil, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error {
				if tc.claims != nil {
					authguard.SetClaims(c, tc.claims)
				}
				return c.Next()
			}, authguard.RequireInternalScope(ScopeByokNotify), func(c fiber.Ctx) error {
				if GetClaims(c) != tc.claims {
					t.Error("GetClaims does not return the claims the guard read")
				}
				return c.SendStatus(200)
			})
			res, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

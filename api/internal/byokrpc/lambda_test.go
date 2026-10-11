package byokrpc

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"
)

func TestTheEnvelopeCarriesTheRefAndTheModeNeverPlaintext(t *testing.T) {
	inv, _, s, _ := setup(t)
	_, _ = Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
		return s.P.Balance(context.Background(), c, tok, byok.Date{})
	})
	req := inv.last.Load().(byok.Request)
	if req.Mode != "live" || req.Provider != "inter" || req.Connection.Sealed != "b1.x" || req.OAuthToken != "t" {
		t.Fatalf("%+v", req)
	}
}

func TestAProviderFailureComesBackAsItsSentinel(t *testing.T) {
	inv, _, s, _ := setup(t)
	inv.unauthorized.Store(5)
	_, err := s.P.Balance(context.Background(), s.Credentials(), byok.Token{Value: "t"}, byok.Date{})
	if !errors.Is(err, byok.ErrUnauthorized) {
		t.Fatalf("%v", err)
	}
}

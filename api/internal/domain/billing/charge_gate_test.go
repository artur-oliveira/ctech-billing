package billing

import (
	"errors"
	"testing"
)

// Spec § 7.1 and § 17 item 3, as amended (no legal gate): one table over rail
// × status × connection × mode × entitlement × suspension × quota.
func TestAuthorizeChargeIsOneTableOverEveryFact(t *testing.T) {
	ok := ChargeGate{Connection: ConnectionFacts{Found: true, Active: true, Collection: true}, Entitled: true}
	cases := []struct {
		name string
		org  Organization
		g    ChargeGate
		want error
	}{
		{"wallet enabled", Organization{PayoutStatus: PayoutEnabled}, ChargeGate{}, nil},
		{"wallet explicit enabled", Organization{PayoutRail: RailWallet, PayoutStatus: PayoutEnabled}, ChargeGate{}, nil},
		{"wallet not enabled", Organization{PayoutStatus: PayoutPendingCustody}, ChargeGate{}, ErrPayoutNotEnabled},
		{"byok live ok", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutProvider: "inter", PayoutStatus: PayoutEnabled}, ok, nil},
		{"byok not enabled", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutNotConfigured}, ok, ErrPayoutNotEnabled},
		{"byok no connection", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Entitled: true}, ErrByokNotConnected},
		{"byok expired", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ConnectionFacts{Found: true, Expired: true, Collection: true}}, ErrByokCertificateExpired},
		{"byok disconnected row", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ConnectionFacts{Found: true, Collection: true}}, ErrByokNotConnected},
		{"byok statement only", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ConnectionFacts{Found: true, Active: true}}, ErrByokScopesMissing},
		{"byok test needs no entitlement", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection}, nil},
		{"byok test ignores the quota", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection, QuotaExceeded: true}, nil},
		{"byok live not entitled", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection}, ErrPayoutNotEnabled},
		{"byok live suspended", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled, SuspendedAt: "2026-10-01T00:00:00Z"}, ok, ErrTenantSuspended},
		{"byok quota refused", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection, Entitled: true, QuotaExceeded: true}, ErrByokQuotaExceeded},
	}
	for _, c := range cases {
		err := c.org.AuthorizeCharge(c.g)
		if (c.want == nil) != (err == nil) || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestAnEmptyRailReadsWallet(t *testing.T) {
	if (&Organization{}).Rail() != RailWallet || (&Organization{PayoutRail: RailBYOK}).Rail() != RailBYOK {
		t.Fatal("rail")
	}
}

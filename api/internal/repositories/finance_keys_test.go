package repositories

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func TestFinanceKeys(t *testing.T) {
	sp, _ := space.ForJob("USER#u1", true)
	nominal := brcal.New(2026, time.March, 31)
	cases := map[string]string{
		BillSK("b1"):                         "BILL#b1",
		OccurrenceSK("r1", nominal):          "OCCURRENCE#r1#2026-03-31",
		RecurrenceSK("r1"):                   "RECURRENCE#r1",
		OpenPK(sp, finance.Payable):          "USER#u1#live#payable",
		OpenSK(nominal, "b1"):                "2026-03-31#b1",
		MaterialisePK(true):                  "live#finance-materialize",
		AutoSettlePK(false):                  "test#finance-autosettle",
		ScheduleSK(nominal, "USER#u1", "r1"): "2026-03-31#USER#u1#r1",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestScheduleSKRoundTrips(t *testing.T) {
	for _, owner := range []string{"USER#u1", "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} {
		sk := ScheduleSK(brcal.New(2026, time.March, 2), owner, "01J9ZX")
		date, o, id, err := ParseScheduleSK(sk)
		if err != nil || date != brcal.New(2026, time.March, 2) || o != owner || id != "01J9ZX" {
			t.Fatalf("%q -> %s %q %q %v", sk, date, o, id, err)
		}
	}
	if _, _, _, err := ParseScheduleSK("garbage"); err == nil {
		t.Fatal("a malformed schedule key was accepted")
	}
}

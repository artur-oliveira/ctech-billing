package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

func TestLedgerAccountIDIsStableAndHidesTheAccount(t *testing.T) {
	a, b := LedgerAccountID("inter", "1234567"), LedgerAccountID("inter", "1234567")
	if a != b || a == "" || len(a) > 64 || strings.Contains(a, "1234567") {
		t.Fatalf("%q %q", a, b)
	}
	if LedgerAccountID("inter", "7654321") == a {
		t.Fatal("another account, another ledger account")
	}
}

func TestPEMChecksRunBeforeTheLambda(t *testing.T) {
	inv := &countingInvoker{}
	svc := newConnectionsForTest(t, inv)
	in := validInput(t)
	in.KeyPEM = otherKeyPEM(t)
	if _, err := svc.Connect(context.Background(), orgSpace(t), in, "u", "r", time.Now()); !errors.Is(err, byok.ErrKeyMismatch) {
		t.Fatalf("%v", err)
	}
	in = validInput(t)
	in.CertPEM, in.KeyPEM = expiredPair(t)
	if _, err := svc.Connect(context.Background(), orgSpace(t), in, "u", "r", time.Now()); !errors.Is(err, byok.ErrCertificateExpired) {
		t.Fatalf("%v", err)
	}
	if inv.calls() != 0 {
		t.Fatal("no Lambda call for a pair the api can refuse itself")
	}
}

func TestConnectRequiresTheAuthorizationAndAnOrganizationSpace(t *testing.T) {
	svc := newConnectionsForTest(t, &countingInvoker{})
	in := validInput(t)
	in.Authorized = false
	if _, err := svc.Connect(context.Background(), orgSpace(t), in, "u", "r", time.Now()); !errors.Is(err, ErrConnectNotAuthorized) {
		t.Fatalf("%v", err)
	}
	if _, err := svc.Connect(context.Background(), personalSpace(t), validInput(t), "u", "r", time.Now()); !errors.Is(err, ErrByokSpaceKind) {
		t.Fatalf("personal: %v", err)
	}
}

func TestBackfillMonthsIsThreeToTwelve(t *testing.T) {
	for _, m := range []int{2, 13} {
		in := validInput(t)
		in.BackfillMonths = m
		if err := in.validate(); !errors.Is(err, ErrBackfillRange) {
			t.Fatalf("%d: %v", m, err)
		}
	}
}

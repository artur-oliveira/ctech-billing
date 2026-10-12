package services

import (
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func TestHookSecretsCompareInConstantTimeAndRotateWithOverlap(t *testing.T) {
	s1, h1 := NewHookSecret()
	s2, h2 := NewHookSecret()
	if len(s1) < 43 || s1 == s2 || strings.ContainsAny(s1, "+/=") {
		t.Fatalf("32 random bytes, base64url: %q", s1)
	}
	hook := &repositories.Hook{SecretHash: h2, PreviousSecretHash: h1, PreviousValidUntil: time.Now().Add(time.Hour)}
	if !HookMatches(hook, s2, time.Now()) || !HookMatches(hook, s1, time.Now()) || HookMatches(hook, s1, time.Now().Add(2*time.Hour)) {
		t.Fatal("current always, previous until its deadline only")
	}
	wrong := s2[:len(s2)-1] + "A"
	if strings.HasSuffix(s2, "A") {
		wrong = s2[:len(s2)-1] + "B"
	}
	if HookMatches(hook, wrong, time.Now()) || HookMatches(hook, "", time.Now()) {
		t.Fatal("wrong secret")
	}
	src, err := os.ReadFile("byok_webhooks.go")
	if err != nil || !strings.Contains(string(src), "subtle.ConstantTimeCompare") {
		t.Fatal("the compare is constant time")
	}
}

func TestTheHookURLCarriesTheSecretInThePath(t *testing.T) {
	got := HookURL("billing-byok-hooks.aoctech.app", "inter", "01HOOK", "s3cr3t", "pix")
	if got != "https://billing-byok-hooks.aoctech.app/w/inter/01HOOK/s3cr3t/pix" {
		t.Fatal(got)
	}
	if !IsOurHookURL(got, "billing-byok-hooks.aoctech.app", "01HOOK") || IsOurHookURL("https://erp.example.com/pix", "billing-byok-hooks.aoctech.app", "01HOOK") ||
		IsOurHookURL("https://billing-byok-hooks.aoctech.app/w/inter/OTHER/x/pix", "billing-byok-hooks.aoctech.app", "01HOOK") {
		t.Fatal("ours means our host and this hook id")
	}
}

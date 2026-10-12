package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// SecretOverlap is how long a rotated webhook secret keeps working.
const SecretOverlap = 24 * time.Hour

// MaxNotifyRefs bounds one notify call (an Inter callback carries a few).
const MaxNotifyRefs = 100

// NewHookSecret is a path secret (32 random bytes, base64url, no padding) and
// the SHA-256 hex the hook row keeps (plan scope decision 8).
func NewHookSecret() (secret, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret = base64.RawURLEncoding.EncodeToString(b)
	return secret, hashSecret(secret)
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HookMatches compares in constant time against the current hash and, until
// its deadline, the previous one.
func HookMatches(h *repositories.Hook, secret string, now time.Time) bool {
	if secret == "" || h == nil {
		return false
	}
	got := []byte(hashSecret(secret))
	cur := subtle.ConstantTimeCompare(got, []byte(h.SecretHash)) == 1
	prev := h.PreviousSecretHash != "" && now.Before(h.PreviousValidUntil) &&
		subtle.ConstantTimeCompare(got, []byte(h.PreviousSecretHash)) == 1
	return cur || prev
}

// HookURL is the URL registered at the provider (spec § 6.2).
func HookURL(host, provider, hookID, secret, product string) string {
	return "https://" + host + "/w/" + provider + "/" + hookID + "/" + secret + "/" + product
}

// IsOurHookURL reports a registered URL that is this hook's on our host (the
// secret may have rotated): never delete or keep a URL that is not.
func IsOurHookURL(url, host, hookID string) bool {
	return host != "" && hookID != "" && strings.HasPrefix(url, "https://"+host+"/w/") &&
		strings.Contains(strings.TrimPrefix(url, "https://"+host), "/"+hookID+"/")
}

// NotifyRequest is the webhook Lambda's call (spec § 6.3). It carries what
// the callback claims and nothing is trusted: amounts are not even in it.
type NotifyRequest struct {
	Provider   string   `json:"provider"`
	HookID     string   `json:"hook_id"`
	Secret     string   `json:"secret"`
	Product    string   `json:"product"`
	Account    string   `json:"account"`
	ChargeRefs []string `json:"charge_refs"`
	PaymentIDs []string `json:"payment_ids"`
}

// ByokWebhooks owns the hook rows and the notify decision.
type ByokWebhooks struct {
	repo      *repositories.ByokRepository
	payments  *repositories.PaymentRepository
	collector *ByokCollector
	guard     cache.Backend
}

func NewByokWebhooks(repo *repositories.ByokRepository, payments *repositories.PaymentRepository, collector *ByokCollector, guard cache.Backend) *ByokWebhooks {
	return &ByokWebhooks{repo: repo, payments: payments, collector: collector, guard: guard}
}

// IssueHook creates a connection's hook, or rotates its secret (the previous
// one valid SecretOverlap more). It returns the plaintext secret once.
func (w *ByokWebhooks) IssueHook(ctx context.Context, conn *byokconn.Connection, hookID string, now time.Time) (string, string, error) {
	secret, hash := NewHookSecret()
	h := &repositories.Hook{ID: hookID, Provider: conn.Provider, AccountOrganizationID: conn.AccountOrganizationID,
		Livemode: conn.Livemode, SecretHash: hash}
	if hookID == repositories.SandboxHookID {
		h.AccountOrganizationID = ""
	}
	if h.ID == "" {
		h.ID = id.New()
	} else if old, err := w.repo.GetHook(ctx, h.ID); err == nil {
		h.PreviousSecretHash, h.PreviousValidUntil = old.SecretHash, now.Add(SecretOverlap).UTC()
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return "", "", err
	}
	if err := w.repo.PutHook(ctx, h, now); err != nil {
		return "", "", err
	}
	return h.ID, secret, nil
}

// alertOnce logs an operator alert at most once an hour per hook.
func (w *ByokWebhooks) alertOnce(ctx context.Context, hookID, what string) {
	key := "byok:hook-alert:" + hookID + ":" + what
	if w.guard != nil {
		if _, seen, _ := w.guard.Get(ctx, key); seen {
			return
		}
		_ = w.guard.Set(ctx, key, []byte("1"), 3600)
	}
	slog.ErrorContext(ctx, "byok webhook refused", "hook_id", hookID, "reason", what)
}

// Notify decides a webhook call (spec § 6.3): 404 for an unknown hook, a
// provider mismatch or a wrong secret; 200 for an account mismatch (a retry
// cannot fix it) and for references ignored, open or confirmed; 503 when a
// re-query failed, so the provider retries.
func (w *ByokWebhooks) Notify(ctx context.Context, in NotifyRequest, now time.Time) int {
	h, err := w.repo.GetHook(ctx, in.HookID)
	if errors.Is(err, repositories.ErrNotFound) {
		w.alertOnce(ctx, "unknown", "unknown_hook")
		return http.StatusNotFound
	}
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if h.Provider != in.Provider || !HookMatches(h, in.Secret, now) {
		w.alertOnce(ctx, h.ID, "secret_or_provider")
		return http.StatusNotFound
	}
	if h.ID != repositories.SandboxHookID {
		// Known deviation (runbook): the shared sandbox hook does not compare
		// the account; the test-mode pointer, written only by billing, decides.
		conn, err := w.repo.GetConnection(ctx, h.AccountOrganizationID, h.Livemode, h.Provider)
		if errors.Is(err, repositories.ErrNotFound) {
			return http.StatusNotFound
		}
		if err != nil {
			return http.StatusServiceUnavailable
		}
		if in.Account != "" && conn.AccountNumber != "" && strings.TrimLeft(in.Account, "0") != strings.TrimLeft(conn.AccountNumber, "0") {
			w.alertOnce(ctx, h.ID, "account_mismatch")
			return http.StatusOK
		}
	}
	status, ignored := http.StatusOK, 0
	for _, ref := range in.ChargeRefs {
		ptr, err := w.payments.ChargeRef(ctx, h.Provider, h.Livemode, ref)
		if errors.Is(err, repositories.ErrNotFound) || (err == nil && h.ID != repositories.SandboxHookID && ptr.AccountOrganizationID != h.AccountOrganizationID) {
			ignored++
			continue
		}
		if err != nil {
			status = http.StatusServiceUnavailable
			continue
		}
		if _, err := w.collector.Confirm(ctx, *ptr, WakeWebhook, now); err != nil {
			slog.WarnContext(ctx, "byok webhook re-query failed", "hook_id", h.ID, "error", err)
			status = http.StatusServiceUnavailable
		}
	}
	slog.InfoContext(ctx, "byok webhook", "hook_id", h.ID, "refs", len(in.ChargeRefs), "ignored", ignored, "status", status)
	return status
}

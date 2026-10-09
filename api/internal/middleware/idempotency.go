package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// IdempotencyHeader is the caller-supplied key.
const IdempotencyHeader = "Idempotency-Key"

// maxKeyLength bounds what is accepted as a key. Long enough for a UUID or a
// caller's own composite id, short enough that it cannot be used as a place to
// store data.
const maxKeyLength = 255

// Idempotency makes every mutating route safe to retry.
//
// It is applied once, at the HTTP layer, to **every** mutating route — not
// per handler. Applied per handler it becomes a thing to remember, and the one
// route where it is forgotten is the one that charges a customer twice.
//
// The key alone is not enough: the request body is hashed too, and a key reused
// with a different body is a 409 rather than a replay. Without the hash, a
// caller with a buggy key generator gets the previous request's response for an
// operation that never ran, and believes it succeeded.
//
// What this does **not** do is serialize concurrent requests with the same key.
// Two simultaneous retries both execute; only the first result is recorded.
// Real mutual exclusion lives in the operations themselves — the invoice
// generation key, the usage sort key — which is where it can actually be
// enforced by the database rather than approximated here.
func Idempotency(store *repositories.IdempotencyRepository, clock func() time.Time) fiber.Handler {
	return idempotency(store, clock, credentialScope)
}

// SpaceIdempotency is Idempotency for the console's finance routes, scoped by the
// resolved space instead of an M2M credential (which a browser session does not
// carry). It must run after ResolveSpace: the key is scoped by the space that
// request was authorized for, so an organization's key and a personal one can
// never collide, and a refused space never reaches the store.
func SpaceIdempotency(store *repositories.IdempotencyRepository, clock func() time.Time) fiber.Handler {
	return idempotency(store, clock, spaceScope)
}

// scopeFunc names the tenant a key belongs to: an owner (an organization id or
// USER#sub), its mode, and whether it could be resolved at all.
type scopeFunc func(c fiber.Ctx) (owner string, livemode, ok bool)

func credentialScope(c fiber.Ctx) (string, bool, bool) {
	cred := GetCredential(c)
	if cred == nil {
		return "", false, false
	}
	return cred.OrganizationID, cred.Livemode, true
}

func spaceScope(c fiber.Ctx) (string, bool, bool) {
	sp := GetSpace(c)
	if sp.IsZero() {
		return "", false, false
	}
	return sp.Owner(), sp.Livemode(), true
}

func idempotency(store *repositories.IdempotencyRepository, clock func() time.Time, scope scopeFunc) fiber.Handler {
	return func(c fiber.Ctx) error {
		key := c.Get(IdempotencyHeader)
		if key == "" {
			return problem.BadRequest(IdempotencyHeader + " header is required").WithCode("idempotency_key_required").Send(c)
		}
		if len(key) > maxKeyLength {
			return problem.BadRequest("Idempotency-Key exceeds the maximum length").WithCode("idempotency_key_too_long").Send(c)
		}
		owner, livemode, ok := scope(c)
		if !ok {
			return problem.Internal("tenant not resolved before idempotency").WithCode("idempotency_tenant_unresolved").Send(c)
		}

		hash := hashRequest(c)
		existing, err := store.Lookup(c.Context(), owner, livemode, key, hash)
		switch {
		case errors.Is(err, repositories.ErrIdempotencyConflict):
			return problem.New(fiber.StatusConflict, problem.TypeIdempotencyConflict,
				"Idempotency Conflict",
				"this Idempotency-Key was already used with a different request body").Send(c)
		case err != nil:
			return problem.Internal("idempotency lookup failed").WithCode("idempotency_lookup_failed").Send(c)
		case existing != nil:
			// A replay. The stored body is returned verbatim, including its status,
			// so the caller cannot tell a retry from the original — which is the
			// entire contract.
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			c.Set("Idempotent-Replay", "true")
			return c.Status(existing.Status).SendString(existing.Response)
		}

		if err := c.Next(); err != nil {
			return err
		}

		// Only successful outcomes are recorded. Replaying a 5xx would make a
		// transient failure permanent for 24 hours; replaying a 4xx would freeze
		// a client's own mistake even after they fixed it.
		status := c.Response().StatusCode()
		if status < 200 || status >= 300 {
			return nil
		}
		record := repositories.IdempotencyRecord{
			Key:         key,
			RequestHash: hash,
			Status:      status,
			Response:    string(c.Response().Body()),
			Route:       c.Method() + " " + c.Route().Path,
		}
		if err := store.Store(c.Context(), owner, livemode, record, clock()); err != nil {
			// The operation already happened. Failing the response now would tell
			// the caller it did not, and their retry would run it again — the exact
			// harm this middleware exists to prevent. The lost record only costs a
			// second execution attempt, which the operation's own key still guards.
			return nil
		}
		return nil
	}
}

// hashRequest fingerprints what makes this request distinct: the route and the
// body. The method and path are included so the same key on two different
// endpoints is a conflict rather than a replay of the wrong operation.
func hashRequest(c fiber.Ctx) string {
	h := sha256.New()
	h.Write([]byte(c.Method()))
	h.Write([]byte{0})
	h.Write([]byte(c.Path()))
	h.Write([]byte{0})
	h.Write(c.Body())
	return hex.EncodeToString(h.Sum(nil))
}

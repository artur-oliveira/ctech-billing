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
	return func(c fiber.Ctx) error {
		key := c.Get(IdempotencyHeader)
		if key == "" {
			return problem.BadRequest("cabeçalho " + IdempotencyHeader + " obrigatório").Send(c)
		}
		if len(key) > maxKeyLength {
			return problem.BadRequest("Idempotency-Key excede o tamanho máximo").Send(c)
		}
		cred := GetCredential(c)
		if cred == nil {
			return problem.Internal("tenant não resolvido antes da idempotência").Send(c)
		}

		hash := hashRequest(c)
		existing, err := store.Lookup(c.Context(), cred.OrganizationID, cred.Livemode, key, hash)
		switch {
		case errors.Is(err, repositories.ErrIdempotencyConflict):
			return problem.New(fiber.StatusConflict, problem.TypeIdempotencyConflict,
				"Idempotency Conflict",
				"esta Idempotency-Key já foi usada com outro corpo de requisição").Send(c)
		case err != nil:
			return problem.Internal("erro ao consultar idempotência").Send(c)
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
		if err := store.Store(c.Context(), cred.OrganizationID, cred.Livemode, record, clock()); err != nil {
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

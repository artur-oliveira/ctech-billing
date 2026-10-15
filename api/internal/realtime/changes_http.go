package realtime

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Headers the changes route reads, the same ones the REST routes use.
const (
	headerSpace = "X-Billing-Space"
	headerMode  = "X-Billing-Mode"

	schemeBearer  = "Bearer"
	schemePayLink = "PayLink"

	// DefaultChangesLimit and MaxChangesLimit bound one page.
	DefaultChangesLimit = 100
	MaxChangesLimit     = 200
)

// ChangesHandler serves the cursor fallback: the same changes, for the same
// scopes, under the same authorization as the socket, over plain HTTP. It is what
// a client falls back to when the socket cannot connect, and it is JSON on
// purpose: it is the debuggable path.
type ChangesHandler struct {
	Authorizers map[string]Authorizer
	Log         ChangeLog
	Now         func() time.Time
}

type changeJSON struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Entity       string `json:"entity"`
	EntityID     string `json:"entity_id"`
	Status       string `json:"status,omitempty"`
	Revision     uint64 `json:"revision"`
	OccurredAtMs int64  `json:"occurred_at_ms"`
}

type changesResponse struct {
	Changes []changeJSON `json:"changes"`
	Head    string       `json:"head"`
	More    bool         `json:"more"`
	// Gap is set instead of changes when the cursor cannot be replayed from:
	// "cursor_too_old". The client reloads what it shows.
	Gap string `json:"gap,omitempty"`
}

func (h *ChangesHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// RegisterChanges mounts GET /realtime/changes.
func RegisterChanges(router fiber.Router, h *ChangesHandler) {
	router.Get("/realtime/changes", h.get)
}

// authFromRequest builds the first-frame equivalent from the headers. A
// credential in the query string is never read: it would end up in access logs.
func authFromRequest(c fiber.Ctx) (*wire.Auth, bool) {
	scheme, credential, ok := strings.Cut(strings.TrimSpace(c.Get(fiber.HeaderAuthorization)), " ")
	credential = strings.TrimSpace(credential)
	if !ok || credential == "" {
		return nil, false
	}
	switch {
	case strings.EqualFold(scheme, schemePayLink):
		return &wire.Auth{PayLink: credential}, true
	case strings.EqualFold(scheme, schemeBearer):
		return &wire.Auth{Token: credential, Space: c.Get(headerSpace), Mode: c.Get(headerMode)}, true
	}
	return nil, false
}

func (h *ChangesHandler) get(c fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	auth, ok := authFromRequest(c)
	if !ok {
		return problem.Unauthorized("credenciais ausentes").Send(c)
	}
	authz := h.Authorizers[Select(auth)]
	if authz == nil {
		return problem.Unauthorized("credenciais ausentes").Send(c)
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	p, err := authz.Authorize(ctx, auth)
	switch {
	case err == nil:
	case errors.Is(err, ErrUnauthorized):
		return problem.Unauthorized("invalid credentials").WithCode("invalid_credentials").Send(c)
	default:
		return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable, "Unavailable", "could not verify access right now").Send(c)
	}

	limit := DefaultChangesLimit
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, MaxChangesLimit)
		}
	}

	head, err := h.Log.Head(ctx, p.Scopes)
	if err != nil {
		slog.WarnContext(ctx, "realtime: head unreadable", "error", err)
		return problem.Internal("could not read changes").Send(c)
	}
	out := changesResponse{Changes: []changeJSON{}, Head: head}

	cursor := c.Query("cursor")
	if cursor == "" {
		return c.JSON(out) // a position, not a request: the client learns where "now" is
	}
	tooOld, err := repositories.CursorTooOld(cursor, h.now())
	switch {
	case errors.Is(err, repositories.ErrBadCursor):
		return problem.BadRequest("invalid cursor").WithCode("invalid_cursor").Send(c)
	case err != nil:
		return problem.Internal("could not read changes").Send(c)
	case tooOld:
		out.Gap = GapCursorTooOld
		return c.JSON(out)
	}

	changes, more, err := h.Log.Since(ctx, p.Scopes, cursor, limit)
	if err != nil {
		slog.WarnContext(ctx, "realtime: changes unreadable", "error", err)
		return problem.Internal("could not read changes").Send(c)
	}
	for _, ch := range changes {
		out.Changes = append(out.Changes, changeJSON{
			ID: ch.ID, Type: ch.Type, Entity: ch.Entity, EntityID: ch.EntityID, Status: ch.Status,
			Revision: ch.Revision, OccurredAtMs: ch.OccurredAt.UnixMilli(),
		})
	}
	out.More = more
	return c.JSON(out)
}

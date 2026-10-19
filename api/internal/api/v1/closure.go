package v1

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/closure"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// closureMFAMaxAge is how recent the owner's MFA proof must be to ask for a
// closure (spec T2). Billing cannot check a password, so MFA is the identity
// check; an owner with no MFA enrols one first (ctech-account's step-up does).
const closureMFAMaxAge = 15 * time.Minute

// closureLinkBody is the body of the public e-mail-link routes: the token is
// the credential.
type closureLinkBody struct {
	RequestID string `json:"request_id"`
	Token     string `json:"token"`
}

type closureRequestBody struct {
	ConfirmationPhrase string `json:"confirmation_phrase"`
}

type closureView struct {
	RequestID  string   `json:"request_id"`
	State      string   `json:"state"`
	ConfirmBy  string   `json:"confirm_by,omitempty"`
	GraceUntil string   `json:"grace_until,omitempty"`
	Blockers   []string `json:"blockers,omitempty"`
}

func viewOf(c *closure.Closure) closureView {
	v := closureView{RequestID: c.ID, State: string(c.State), Blockers: c.Blockers}
	if !c.ConfirmBy.IsZero() && c.State == closure.AwaitingConfirmation {
		v.ConfirmBy = c.ConfirmBy.UTC().Format(time.RFC3339)
	}
	if !c.GraceUntil.IsZero() {
		v.GraceUntil = c.GraceUntil.UTC().Format(time.RFC3339)
	}
	return v
}

type closureHandlers struct {
	svc *services.TenantClosures
}

// registerClosure mounts the closure routes when the service is wired (the
// feature on). The console routes sit OUTSIDE the closing gate: asking for the
// status or an export of a tenant that is closing must keep working. They need no
// merchant tenant resolution either (the service finds the workspace's tenants).
func registerClosure(v1 fiber.Router, d Deps, auth fiber.Handler, spaces fiber.Handler) {
	if d.Closures == nil {
		return
	}
	h := &closureHandlers{svc: d.Closures}
	console := v1.Group("/console/closure", auth, middleware.RequireUserSession(), spaces)
	console.Get("", middleware.RequireUserScope(middleware.ScopeOrganizationRead), h.status)
	console.Get("/blockers", middleware.RequireUserScope(middleware.ScopeOrganizationRead), h.blockers)
	console.Post("", middleware.RequireUserScope(middleware.ScopeOrganizationWrite), h.request)

	v1.Post("/closure/confirm", h.confirm)
	v1.Post("/closure/cancel", h.cancel)
}

func (h *closureHandlers) owner(c fiber.Ctx) (acct string, ok bool, err error) {
	sp := middleware.GetSpace(c)
	if sp.OrganizationID() == "" {
		return "", false, closure.ErrNotFound // a personal space has no billing tenant
	}
	return sp.OrganizationID(), sp.Role() == "owner", nil
}

func (h *closureHandlers) status(c fiber.Ctx) error {
	acct, owner, err := h.owner(c)
	if err != nil || !owner {
		return h.refuse(c, err, owner)
	}
	cl, err := h.svc.Status(c.Context(), acct)
	if errors.Is(err, closure.ErrNotFound) {
		return problem.NotFound("no closure in progress").Send(c)
	}
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(viewOf(cl))
}

func (h *closureHandlers) blockers(c fiber.Ctx) error {
	acct, owner, err := h.owner(c)
	if err != nil || !owner {
		return h.refuse(c, err, owner)
	}
	bs, err := h.svc.Blockers(c.Context(), acct)
	if errors.Is(err, closure.ErrNotFound) {
		return problem.NotFound("this workspace has no billing account").Send(c)
	}
	if err != nil {
		return fail(c, err)
	}
	if bs == nil {
		bs = []services.ClosureBlocker{}
	}
	return c.JSON(fiber.Map{"blockers": bs})
}

func (h *closureHandlers) refuse(c fiber.Ctx, err error, owner bool) error {
	switch {
	case errors.Is(err, closure.ErrNotFound):
		return problem.NotFound("this workspace has no billing account").Send(c)
	case !owner:
		return problem.Forbidden("only an owner of the workspace can close its billing account").WithCode("owner_required").Send(c)
	}
	return fail(c, err)
}

func (h *closureHandlers) request(c fiber.Ctx) error {
	acct, owner, err := h.owner(c)
	if err != nil || !owner {
		return h.refuse(c, err, owner)
	}
	var body closureRequestBody
	if err := c.Bind().Body(&body); err != nil || body.ConfirmationPhrase != closure.Phrase {
		return problem.BadRequest(`confirmation_phrase must be exactly "` + closure.Phrase + `"`).WithCode("phrase_mismatch").Send(c)
	}
	cl := middleware.GetClaims(c)
	// The claim is wall-clock: compared with the real clock, never a pinned one.
	if cl.LastMFAAt == 0 || time.Since(time.Unix(cl.LastMFAAt, 0)) > closureMFAMaxAge {
		return problem.New(fiber.StatusForbidden, "/problems/step-up-required", "Step-up required",
			"confirm with a recent MFA proof to close the billing account").WithCode("step_up_required").Send(c)
	}
	created, err := h.svc.Request(c.Context(), cl.Sub, acct, owner)
	var blocked *services.ClosureBlockedError
	switch {
	case errors.As(err, &blocked):
		return problem.New(fiber.StatusConflict, "/problems/closure-blocked", "Closure blocked", "resolve the blockers first").
			WithCode("closure_blocked").WithExtra(problem.ExtraBlockers, blocked.Blockers).Send(c)
	case errors.Is(err, closure.ErrOpenClosure):
		return problem.Conflict("a closure is already in progress").WithCode("closure_open").Send(c)
	case errors.Is(err, closure.ErrTenantZero):
		return problem.Forbidden("this billing account cannot be closed").WithCode("closure_not_allowed").Send(c)
	case errors.Is(err, closure.ErrNotFound):
		return problem.NotFound("this workspace has no billing account").Send(c)
	case err != nil:
		return fail(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(viewOf(created))
}

func (h *closureHandlers) confirm(c fiber.Ctx) error {
	var body closureLinkBody
	if err := c.Bind().Body(&body); err != nil || body.RequestID == "" || body.Token == "" {
		return problem.BadRequest("request_id and token are required").Send(c)
	}
	cl, err := h.svc.Confirm(c.Context(), body.RequestID, body.Token)
	return h.linkResult(c, cl, err)
}

func (h *closureHandlers) cancel(c fiber.Ctx) error {
	var body closureLinkBody
	if err := c.Bind().Body(&body); err != nil || body.RequestID == "" || body.Token == "" {
		return problem.BadRequest("request_id and token are required").Send(c)
	}
	cl, err := h.svc.Cancel(c.Context(), body.RequestID, body.Token)
	return h.linkResult(c, cl, err)
}

func (h *closureHandlers) linkResult(c fiber.Ctx, cl *closure.Closure, err error) error {
	switch {
	case err == nil:
		return c.JSON(viewOf(cl))
	case errors.Is(err, closure.ErrInvalidToken), errors.Is(err, closure.ErrExpired):
		return problem.Unauthorized("this link is invalid or has expired").WithCode("invalid_link").Send(c)
	case errors.Is(err, closure.ErrNotCancellable), errors.Is(err, closure.ErrWrongState):
		return problem.Conflict("this request is no longer in a state that allows that").WithCode("closure_state").Send(c)
	}
	return fail(c, err)
}

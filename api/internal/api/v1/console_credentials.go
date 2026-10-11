package v1

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

type credentialResponse struct {
	ClientID      string `json:"client_id"`
	Livemode      bool   `json:"livemode"`
	Active        bool   `json:"active"`
	Provisioned   bool   `json:"provisioned"`
	ConnectedBy   string `json:"connected_by,omitempty"`
	ConnectedByMe bool   `json:"connected_by_me"`
	ConnectedAt   string `json:"connected_at,omitempty"`
	Description   string `json:"description,omitempty"`
}

func newCredentialResponse(c *billing.APICredential, me string) credentialResponse {
	return credentialResponse{
		ClientID: c.ClientID, Livemode: c.Livemode, Active: c.Active, Provisioned: c.Provisioned(),
		ConnectedBy: c.ConnectedBy, ConnectedByMe: c.ConnectedBy != "" && c.ConnectedBy == me,
		ConnectedAt: c.ConnectedAt, Description: c.Description,
	}
}

type credentialListResponse struct {
	Data []credentialResponse `json:"data"`
	// MachineScopes is what the console's link to the developer page
	// preselects: the scopes a connectable client may hold.
	MachineScopes []string `json:"machine_scopes"`
}

type connectCredentialRequest struct {
	ClientID string `json:"client_id"`
}

func me(c fiber.Ctx) string {
	if cl := middleware.GetClaims(c); cl != nil {
		return cl.Sub
	}
	return ""
}

func (h *consoleHandlers) listCredentials(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	creds, err := h.creds.ListForTenant(c.Context(), t.OrganizationID, t.Livemode)
	if err != nil {
		return fail(c, err)
	}
	out := credentialListResponse{Data: make([]credentialResponse, 0, len(creds)), MachineScopes: h.connector.MachineScopes()}
	for i := range creds {
		out.Data = append(out.Data, newCredentialResponse(&creds[i], me(c)))
	}
	return c.JSON(out)
}

func (h *consoleHandlers) connectCredential(c fiber.Ctx) error {
	if isSuspended(c) {
		return refuseSuspended(c)
	}
	var req connectCredentialRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.clientID("client_id", req.ClientID)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	t := middleware.GetTenant(c)
	cred, created, err := h.connector.Connect(c.Context(), services.ConnectRequest{
		OrganizationID: t.OrganizationID, Livemode: t.Livemode, ClientID: req.ClientID,
		UserID: me(c), RequestID: middleware.GetRequestID(c), Now: h.now(),
	})
	var refused *services.ConnectRefusedError
	switch {
	case errors.As(err, &refused):
		return problem.Unprocessable("the client cannot be connected").WithCode(string(refused.Code)).Send(c)
	case errors.Is(err, services.ErrAccountUnavailable):
		return problem.New(http.StatusServiceUnavailable, problem.TypeAccountUnavailable, "Account Unavailable",
			"CTech Account did not answer; try again").WithCause(err).WithCode("account_unavailable").Send(c)
	case err != nil:
		return fail(c, err)
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	return c.Status(status).JSON(newCredentialResponse(cred, me(c)))
}

// disconnectCredential is allowed on a suspended tenant (scope decision 1).
func (h *consoleHandlers) disconnectCredential(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	cred, err := h.creds.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("client_id"))
	if err != nil {
		return fail(c, err)
	}
	err = h.creds.Disconnect(c.Context(), cred, actorOfUser(c), middleware.GetRequestID(c), h.now())
	switch {
	case errors.Is(err, repositories.ErrCredentialProvisioned):
		return problem.Conflict("this credential is managed by CTech").WithCode("credential_provisioned").Send(c)
	case err != nil:
		return fail(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

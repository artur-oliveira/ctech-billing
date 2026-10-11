package v1

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// The merchant application (spec § 4) on the console. The applicant's three
// routes run without a tenant (RequireMerchantRole only); the review routes run
// behind the merchant chain and RequireTenantZero.

func applicationFail(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrCompanyNotInOrganization):
		return problem.Validation([]problem.FieldError{fieldErr("company_id", "not_found", "not one of the organization's companies")}).Send(c)
	case errors.Is(err, accountclient.ErrOrganizationNotFound):
		// ctech-account does not know the organization: the same body as an
		// unknown space, never the 503 that says "try again".
		return problem.New(fiber.StatusNotFound, problem.TypeSpaceNotFound, "Space not found",
			"space not found").WithCode("space_not_found").Send(c)
	case errors.Is(err, services.ErrCompaniesUnavailable):
		return problem.New(fiber.StatusServiceUnavailable, problem.TypeCompaniesUnavailable, "Companies unavailable",
			"could not read the organization's companies right now").WithCode("companies_unavailable").Send(c)
	}
	return fail(c, err)
}

func (h *consoleHandlers) merchantCompanies(c fiber.Ctx) error {
	list, err := h.applications.Companies(c.Context(), middleware.GetMerchantAccess(c).AccountOrganizationID)
	if err != nil {
		return applicationFail(c, err)
	}
	out := make([]companyResponse, 0, len(list.Companies))
	for _, co := range list.Companies {
		out = append(out, companyResponse{ID: co.ID, LegalName: co.LegalName, TradeName: co.TradeName, TaxID: co.TaxID})
	}
	return c.JSON(companiesResponse{Data: out, Truncated: list.Truncated})
}

func (h *consoleHandlers) getMerchantApplication(c fiber.Ctx) error {
	app, err := h.applications.Current(c.Context(), middleware.GetMerchantAccess(c).AccountOrganizationID)
	if errors.Is(err, repositories.ErrNotFound) {
		return problem.NotFound("this organization has not applied").WithCode("no_application").Send(c)
	}
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(newApplicationResponse(app))
}

// httpsURL: an absolute https URL with a host and no credentials.
func httpsURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func (h *consoleHandlers) applyMerchant(c fiber.Ctx) error {
	var req applyRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	req.Description, req.Website, req.ContactEmail = strings.TrimSpace(req.Description), strings.TrimSpace(req.Website), strings.TrimSpace(req.ContactEmail)
	var ch checks
	ch.id("company_id", req.CompanyID, true)
	ch.text("description", req.Description, true, limits.ApplicationDescription)
	if n := utf8.RuneCountInString(req.Description); n > 0 && n < limits.ApplicationDescriptionMin {
		ch.fail("description", "too_short", "at least 20 characters", "min", limits.ApplicationDescriptionMin)
	}
	if req.Website != "" {
		ch.text("website", req.Website, false, limits.Website)
		if !httpsURL(req.Website) {
			ch.fail("website", "invalid_url", "an https address")
		}
	}
	if !billing.MonthlyVolume(req.ExpectedMonthlyVolume).Valid() {
		ch.fail("expected_monthly_volume", "unsupported_value", "not one of the listed ranges")
	}
	ch.email("contact_email", req.ContactEmail, true)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}

	acc := middleware.GetMerchantAccess(c)
	sub := middleware.GetClaims(c).Sub
	name, err := h.organizationName(c, sub, acc.AccountOrganizationID)
	if err != nil {
		return problem.New(fiber.StatusServiceUnavailable, problem.TypeSpaceUnavailable,
			"Space unavailable", "could not verify access right now").Send(c)
	}
	app, created, err := h.applications.Apply(c.Context(), services.ApplyInput{
		AccountOrganizationID: acc.AccountOrganizationID, OrganizationName: name, UserID: sub,
		CompanyID: req.CompanyID, Description: req.Description, Website: req.Website,
		Volume: billing.MonthlyVolume(req.ExpectedMonthlyVolume), ContactEmail: req.ContactEmail,
		RequestID: middleware.GetRequestID(c),
	}, h.now())
	if err != nil {
		return applicationFail(c, err)
	}
	status := fiber.StatusOK
	if created {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(newApplicationResponse(app))
}

// organizationName is the account organization's display name, from the same
// list the session reads (the space resolver carries no names).
func (h *consoleHandlers) organizationName(c fiber.Ctx, sub, accountOrgID string) (string, error) {
	if h.spaces == nil {
		return "", errors.New("no organization lister")
	}
	ws, err := h.spaces.Organizations(c.Context(), sub)
	if err != nil {
		return "", err
	}
	for _, w := range ws {
		if w.ID == accountOrgID {
			return w.DisplayName, nil
		}
	}
	return "", errors.New("organization not in the person's list")
}

func (h *consoleHandlers) listApplications(c fiber.Ctx) error {
	status := billing.ApplicationStatus(c.Query("status", string(billing.ApplicationPending)))
	if !status.Valid() {
		return problem.BadRequest("status must be pending, approved or rejected").WithCode("invalid_status").Send(c)
	}
	start, err := repositories.DecodeCursor(c.Query("cursor"))
	if err != nil {
		return problem.BadRequest("invalid cursor").WithCode("invalid_cursor").Send(c)
	}
	page, err := h.applications.List(c.Context(), status, pageLimit, start)
	if err != nil {
		return fail(c, err)
	}
	out := make([]applicationResponse, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, newApplicationResponse(&page.Items[i]))
	}
	return c.JSON(pageOf(out, page.LastEvaluatedKey))
}

func (h *consoleHandlers) approveApplication(c fiber.Ctx) error {
	app, err := h.applications.Approve(c.Context(), c.Params("id"), actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return applicationFail(c, err)
	}
	return c.JSON(newApplicationResponse(app))
}

func (h *consoleHandlers) rejectApplication(c fiber.Ctx) error {
	var req rejectRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	req.Reason = strings.TrimSpace(req.Reason)
	var ch checks
	ch.text("reason", req.Reason, true, limits.Reason)
	if n := utf8.RuneCountInString(req.Reason); n > 0 && n < limits.RejectionReasonMin {
		ch.fail("reason", "too_short", "at least 10 characters", "min", limits.RejectionReasonMin)
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	app, err := h.applications.Reject(c.Context(), c.Params("id"), req.Reason, actorOfUser(c), middleware.GetRequestID(c), h.now())
	if err != nil {
		return applicationFail(c, err)
	}
	return c.JSON(newApplicationResponse(app))
}

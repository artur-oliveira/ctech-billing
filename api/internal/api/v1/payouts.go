package v1

import (
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// resolveAfter is how old a processing payout must be before a read asks the
// bank about it again: the wallet polls, and the poll is what finishes it.
const resolveAfter = 5 * time.Second

type createPayoutRequest struct {
	Reference   string        `json:"reference"`
	CPF         string        `json:"cpf"`
	AmountCents billing.Cents `json:"amount_cents"`
	Message     string        `json:"message"`
}

type payoutResponse struct {
	ID            string              `json:"id"`
	Reference     string              `json:"reference"`
	Status        billing.PayoutState `json:"status"`
	AmountCents   billing.Cents       `json:"amount_cents"`
	PaymentID     string              `json:"payment_id,omitempty"`
	FailureReason string              `json:"failure_reason,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}

func newPayoutResponse(p *billing.Payout) payoutResponse {
	return payoutResponse{ID: p.ID, Reference: p.Reference, Status: p.Status, AmountCents: p.Amount, PaymentID: p.PaymentID,
		FailureReason: p.FailureReason, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

type payoutHandlers struct {
	svc   *services.Payouts
	clock func() time.Time
}

// create pays a Pix key (POST /v1.0/payouts): CTech's own services only. 201
// when it reserved the payout, 200 when the reference already had one. The
// answer is the payout as it stands, normally still processing: the bank's word
// arrives through the reads.
func (h *payoutHandlers) create(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	var req createPayoutRequest
	if err := c.Bind().Body(&req); err != nil {
		return problem.BadRequest("invalid request body").WithCode("invalid_body").Send(c)
	}
	ch := &checks{}
	ch.text("reference", req.Reference, true, 64)
	ch.text("cpf", req.CPF, true, 20)
	ch.amount("amount_cents", req.AmountCents)
	ch.text("message", req.Message, false, limits.Description)
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	owner := ""
	if cred := middleware.GetCredential(c); cred != nil {
		owner = cred.OwnerKey
	}
	p, created, err := h.svc.Create(c.Context(), t.OrganizationID, t.Livemode, services.PayoutInput{
		Reference: req.Reference, CPF: req.CPF, AmountCents: int64(req.AmountCents), Message: req.Message, OwnerKey: owner}, h.clock())
	if err != nil {
		return fail(c, err)
	}
	// The bank settles a Pix in seconds: ask again soon, in the background, so the
	// caller is told when it happens and not at the next sweep.
	h.svc.FollowSoon(p)
	status := fiber.StatusOK
	if created {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(newPayoutResponse(p))
}

func (h *payoutHandlers) get(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	p, err := h.svc.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	return h.reply(c, p, err)
}

func (h *payoutHandlers) getByReference(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	p, err := h.svc.GetByReference(c.Context(), t.OrganizationID, t.Livemode, c.Params("reference"))
	return h.reply(c, p, err)
}

// reply asks the bank once more about a payout still processing before it
// answers, so the caller's polling is what finishes the payout without waiting
// for the hourly sweep. A failure to ask leaves the stored answer.
func (h *payoutHandlers) reply(c fiber.Ctx, p *billing.Payout, err error) error {
	if err != nil {
		return fail(c, err)
	}
	if !p.Final() && h.clock().Sub(p.UpdatedAt) >= resolveAfter {
		_ = h.svc.Resolve(c.Context(), p, h.clock()) // an error leaves the stored answer
	}
	return c.JSON(newPayoutResponse(p))
}

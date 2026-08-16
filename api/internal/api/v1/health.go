package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// health is a dependency-free liveness probe.
//
// It deliberately checks nothing downstream. A health endpoint that fails when
// DynamoDB is slow takes the instance out of the load balancer during exactly
// the incident when capacity matters most. A dependency report is a different
// endpoint, added when there is an ALB target group that needs one.
func (h *handlers) health(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status": "ok",
		// The service's own idea of today, in São Paulo. It is here because a
		// billing service running with the wrong timezone bills on the wrong day,
		// and this is the cheapest way to notice.
		"today":    h.today().String(),
		"timezone": brcal.Location.String(),
	})
}

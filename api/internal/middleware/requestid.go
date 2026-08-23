package middleware

import (
	"github.com/gofiber/fiber/v3"
	fiberobs "gopkg.aoctech.app/api-commons/observability/fiber"
)

// RequestIDHeader is the correlation header, read from the caller when present
// so a trace survives the hop, generated when it is not.
const RequestIDHeader = fiberobs.RequestIDHeader

// RequestID assigns every request a correlation id and echoes it back.
//
// It costs one line of middleware and it is the difference between a support
// ticket that can be answered and one that cannot: the id appears in the
// response, in the audit trail of anything the request changed, and in the logs
// (assessment § 13). Without it, "my invoice did not get paid" is unanswerable.
func RequestID() fiber.Handler {
	return fiberobs.RequestID(fiberobs.RequestIDConfig{LocalsKey: RequestIDKey})
}

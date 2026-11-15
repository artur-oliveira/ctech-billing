package billing

import (
	"errors"
	"fmt"

	"gopkg.aoctech.app/api-commons/webhooks"
)

// ErrWebhookHostNotAllowed reports a URL whose host is not on the public
// internet. Since merchants register their own endpoints (spec § 5.2), billing
// would otherwise make signed requests, on a schedule, to whatever address a
// merchant names, CTech's own private network included.
var ErrWebhookHostNotAllowed = errors.New("webhook host is not a public address")

// CheckWebhookURL is the console's rule for a merchant endpoint, on top of
// WebhookEndpoint.Validate: webhooks.CheckURL (https, a host, no credentials, a
// public host), reported with this package's errors. Resolution is checked again
// at dial time by the guarded client, where DNS can no longer change the answer.
func CheckWebhookURL(raw string) error {
	err := webhooks.CheckURL(raw)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, webhooks.ErrHostNotAllowed):
		return fmt.Errorf("%w: %w", ErrWebhookHostNotAllowed, err)
	}
	return fmt.Errorf("%w: %w", ErrInvalidEndpoint, err)
}

package services

import (
	"context"
	"time"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/erasure"
)

// alertTimeout bounds the SNS publish so a slow topic cannot hold the consumer.
const alertTimeout = 3 * time.Second

// AlertingPurge wraps an erasure purge so a failure also tells an operator: an
// account-deletion request that cannot finish is a legal deadline (LGPD), and
// the consumer only logs it and leaves the message for redelivery. The error is
// returned unchanged, so redelivery behaves exactly as before. A redelivered
// failure alerts again; the queue's visibility timeout paces it.
// Same shape as ctech-dfe's services.AlertingPurge.
func AlertingPurge(purge erasure.PurgeFunc, p alerts.Publisher) erasure.PurgeFunc {
	return func(ctx context.Context, m erasure.Message) (erasure.Ack, error) {
		ack, err := purge(ctx, m)
		if err != nil {
			actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), alertTimeout)
			defer cancel()
			p.Alert(actx, alerts.Alert{
				Job:     "erasure-purge-failed",
				Summary: "An account-deletion purge failed; the message stays on the queue and will be redelivered.",
				Err:     err,
				Detail:  "request_id: " + m.RequestID,
			})
		}
		return ack, err
	}
}

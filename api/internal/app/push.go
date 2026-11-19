package app

import (
	"context"
	"errors"
	"fmt"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"gopkg.aoctech.app/api-commons/pushsvc"

	v1 "gopkg.aoctech.app/billing/api/internal/api/v1"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/pendingpush"
)

// offSender is the PushJobSender when no sender function is configured: the test route then answers an error.
type offSender struct{}

func (offSender) Send(context.Context, pushsvc.Job) error {
	return errors.New("push: PUSH_SENDER_FUNCTION is not set")
}

// buildPush builds the Web Push routes' dependencies. Without PUSH_VAPID_PUBLIC_KEY the service is off (config answers
// enabled=false, subscribing answers 503), so a deploy before the operator writes the keys changes nothing.
func buildPush(ctx context.Context, cfg *config.Config, db *dynamodb.Client) (*v1.PushDeps, error) {
	prefs := pendingpush.NewService(pushsvc.NewReminderStore(db, cfg.TablePrefix))
	svc := pushsvc.NewService(pushsvc.NewStore(db, cfg.TablePrefix), cfg.PushVAPIDPublicKey)
	var sender v1.PushJobSender = offSender{}
	if cfg.PushSenderFunction != "" {
		awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
		if err != nil {
			return nil, fmt.Errorf("aws config: %w", err)
		}
		sender = pushsvc.NewInvoker(lambda.NewFromConfig(awsCfg), cfg.PushSenderFunction)
	}
	return &v1.PushDeps{Service: svc, Prefs: prefs, Sender: sender}, nil
}

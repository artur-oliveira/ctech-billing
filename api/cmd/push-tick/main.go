// Command push-tick is the scheduled Lambda behind Billing's Web Push reminder. EventBridge Scheduler runs it every 15
// minutes (São Paulo time); it reads the reminders due in the slot, applies the Billing rule (pendingpush) and hands each
// push to the push-sender Lambda. It holds no VAPID key: sending is the sender's job.
// docs/specs/2026-11-18-pending-reminder-web-push.md.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api-commons/pushsvc"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/pendingpush"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	lambda.Start(handler)
}

func handler(ctx context.Context) error {
	prefix, sender := os.Getenv("TABLE_PREFIX"), os.Getenv("PUSH_SENDER_FUNCTION")
	if sender == "" {
		slog.WarnContext(ctx, "push tick: PUSH_SENDER_FUNCTION is not set; nothing sent")
		return nil
	}
	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	db := dynamodb.NewFromConfig(awsCfg)
	// The repositories read only the table prefix of the config.
	recs := repositories.NewRecurrenceRepository(db, &config.Config{TablePrefix: prefix})
	reminders := pushsvc.NewReminderStore(db, prefix)
	rules := pendingpush.NewRules(newCounter(recs))
	invoker := pushsvc.NewInvoker(awslambda.NewFromConfig(awsCfg), sender)
	return pushsvc.NewTick(reminders, pushsvc.NewStore(db, prefix), rules, invoker).Run(ctx, time.Now())
}

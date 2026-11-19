// Command push-sender sends a Web Push notification Billing's tick (or the test route) rendered. It is go-common's
// pushsvc sender core over Billing's push table: it reads each recipient's devices, sends, and deletes a device the push
// service says is gone. Without the VAPID parameters it does nothing and does not fail (push is off until an operator
// writes them). docs/specs/2026-11-18-pending-reminder-web-push.md.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/pushsvc"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		panic("aws config: " + err.Error())
	}
	pub, err := alerts.New(ctx, awsCfg.Region, os.Getenv("ALERTS_TOPIC_ARN"), "billing-push", os.Getenv("ENVIRONMENT"))
	if err != nil {
		slog.WarnContext(ctx, "alerts: publisher unavailable, operator alerts are log-only", "err", err)
		pub = alerts.Nop{}
	}
	h := pushsvc.NewHandler(pushsvc.NewStore(dynamodb.NewFromConfig(awsCfg), os.Getenv("TABLE_PREFIX")), nil, pub)
	lambda.Start(pushsvc.Lambda(h, ssm.NewFromConfig(awsCfg), pushsvc.Paths{
		Private: os.Getenv("PUSH_VAPID_PRIVATE_KEY_SSM_PATH"), Previous: os.Getenv("PUSH_VAPID_PRIVATE_KEY_PREVIOUS_SSM_PATH"),
		Public: os.Getenv("PUSH_VAPID_PUBLIC_KEY_SSM_PATH"), Subject: os.Getenv("PUSH_SUBJECT_SSM_PATH"),
	}))
}

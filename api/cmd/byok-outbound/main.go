// Command byok-outbound is billing's only connection to a merchant's bank
// (spec 2026-10-11 § 4.1). Not VPC-attached (IPv4 egress without NAT), one op
// per invocation, called synchronously by the api and cmd/byok. It holds the
// credentials key (read once at cold start) and accepts only the known ops
// (byok.GroupOf); SimulatePayment only against a sandbox.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/inter"

	"gopkg.aoctech.app/billing/api/internal/byokseal"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	env := os.Getenv("ENVIRONMENT")
	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("aws config", "error", err)
		os.Exit(1)
	}
	notify, aerr := alerts.New(ctx, awsCfg.Region, os.Getenv("ALERTS_TOPIC_ARN"), "billing", env)
	if aerr != nil {
		notify = alerts.Nop{}
	}
	params := ssm.NewFromConfig(awsCfg)
	key, err := readParam(ctx, params, os.Getenv("BYOK_CREDENTIALS_KEY_PARAM"))
	if err == nil {
		_, err = byokseal.New(key)
	}
	if err != nil {
		// No development fallback (ADR 0017): every op fails as "provider down".
		notify.Alert(ctx, alerts.Alert{Job: "byok-outbound-startup", Summary: "byok outbound Lambda did not start: credentials key unreadable", Err: err})
		slog.Error("credentials key unreadable")
		os.Exit(1)
	}
	sealer, _ := byokseal.New(key)
	h := &handler{
		sealer:    sealer,
		providers: map[string]byok.Provider{"inter": inter.NewProvider(inter.NewClient(inter.WithLogger(slog.Default())))},
		rules:     rules{forceSandbox: os.Getenv("BYOK_FORCE_SANDBOX") == "true", payoutOrgs: parseOrgs(os.Getenv("BYOK_PAYOUT_ACCOUNT_ORGS"))},
		sandbox: sandboxLoader(params, os.Getenv("BYOK_SANDBOX_CREDENTIAL_PARAMS"),
			os.Getenv("BYOK_SANDBOX_ACCOUNT_PARAM"), os.Getenv("BYOK_SANDBOX_PIX_KEY_PARAM")),
		bundles: newBundleCache(),
	}
	lambda.Start(h.handle)
}

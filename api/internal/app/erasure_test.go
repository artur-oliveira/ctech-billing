package app

import (
	"context"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/config"
)

func TestRevocationURLDropsTheLogicalDB(t *testing.T) {
	for in, want := range map[string]string{
		"redis://valkey.internal:6379/3":          "redis://valkey.internal:6379",
		"rediss://:secret@valkey.internal:6379/3": "rediss://:secret@valkey.internal:6379",
		"redis://valkey.internal:6379":            "redis://valkey.internal:6379",
	} {
		got, err := revocationURL(in)
		if err != nil || got != want {
			t.Errorf("revocationURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := revocationURL("valkey.internal"); err == nil {
		t.Error("a URL with no host must be refused, not turned into a DB 0 connection to nowhere")
	}
}

func TestPortalInvoicesURLIsTheCheckoutsOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://billing.aoctech.app/checkout":     "https://billing.aoctech.app/invoices",
		"https://billing-dev.aoctech.app/checkout": "https://billing-dev.aoctech.app/invoices",
		"": "",
	} {
		if got := portalInvoicesURL(in); got != want {
			t.Errorf("portalInvoicesURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// terraform/billing/erasure.tf names the queue "${local.name}-erasure" with
// local.name = "${var.environment}-ctech-billing", and the client parameters
// "${local.ssm_prefix}/erasure-client-{id,secret}" with
// local.ssm_prefix = "/ctech-billing/${var.environment}/billing".
// Change both sides or neither.
func TestErasureNamesMatchTerraform(t *testing.T) {
	if got := erasureQueueName("prod"); got != "prod-ctech-billing-erasure" {
		t.Fatalf("erasureQueueName(prod) = %q", got)
	}
	id, secret := erasureClientParams("prod")
	if id != "/ctech-billing/prod/billing/erasure-client-id" || secret != "/ctech-billing/prod/billing/erasure-client-secret" {
		t.Fatalf("erasureClientParams(prod) = %q, %q", id, secret)
	}
}

// A laptop with real AWS credentials and a local DynamoDB must never consume
// dev's queue into a local table.
func TestNoConsumerWithoutCtechAccountOrAgainstALocalTable(t *testing.T) {
	full := config.Config{Env: "dev", AccountBaseURL: "https://a", AccountTokenURL: "https://a/t",
		ErasureClientID: "billing-erasure", ErasureClientSecret: "s"}
	for name, cfg := range map[string]config.Config{
		"no ctech-account": {Env: "dev"},
		"local dynamodb":   func() config.Config { c := full; c.DynamoDBEndpoint = "http://localhost:8124"; return c }(),
	} {
		c, err := BuildErasureConsumer(context.Background(), &cfg, nil, nil)
		if c != nil || err != nil {
			t.Errorf("%s: consumer %v, err %v; want neither", name, c, err)
		}
	}
}

func TestAnUnsetErasureClientIsRefused(t *testing.T) {
	for _, pair := range [][2]string{{"", ""}, {"SET-OUT-OF-BAND", "SET-OUT-OF-BAND"}, {"billing-erasure", ""}} {
		if err := usableErasureClient(pair[0], pair[1]); err == nil {
			t.Errorf("client %q/%q accepted; a placeholder must never mint a token", pair[0], pair[1])
		}
	}
	if err := usableErasureClient("billing-erasure", "s3cret"); err != nil {
		t.Fatalf("a real client was refused: %v", err)
	}
}

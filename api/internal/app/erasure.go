package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/dataexport"
	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/api-commons/oauth2client"
	"gopkg.aoctech.app/api-commons/resilient"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/jobs"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// revocationURL is VALKEY_URL with its logical-DB path removed. ctech-account
// writes the token revocation list in DB 0 of the shared server (jwtverify,
// saga protocol § 5). Billing's own cache lives in DB 3, where those keys are
// invisible. Derived rather than configured: the userdata is at its 16 KiB
// ceiling (terraform/README).
func revocationURL(valkeyURL string) (string, error) {
	// The URL can carry the password, so no error here quotes it, and url.Parse's
	// own error (which does) is dropped.
	u, err := url.Parse(valkeyURL)
	if err != nil {
		return "", errors.New("valkey url is not parseable")
	}
	if u.Host == "" {
		return "", errors.New("valkey url has no host")
	}
	u.Path, u.RawPath = "", ""
	return u.String(), nil
}

// revocationBackend is the DB 0 connection the verifier checks revoked subjects
// against. It is nil when there is no Valkey: the check is then off, and a
// locked person is still refused by the erasure lock.
func revocationBackend(cfg *config.Config) cache.Backend {
	if cfg.RedisURL == "" {
		return nil
	}
	raw, err := revocationURL(cfg.RedisURL)
	if err != nil {
		slog.Error("VALKEY_URL unusable for the revocation list — revoked tokens are accepted until they expire", "error", err)
		return nil
	}
	backend, err := cache.NewRedisBackend(raw)
	if err != nil {
		slog.Error("valkey DB 0 unavailable — revoked tokens are accepted until they expire", "error", err)
		return nil
	}
	return backend
}

// portalInvoicesURL is the portal's invoice list, where a person settles what
// blocks their erasure. Derived from CHECKOUT_BASE_URL's origin, because both
// pages are the same static site (ADR 0013), and so the userdata carries
// nothing new.
func portalInvoicesURL(checkoutBaseURL string) string {
	u, err := url.Parse(checkoutBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/invoices"
}

// newEraser is the one way the API and the consumer build the Eraser, so both
// look for the same customer in the same tenant.
func newEraser(db *dynamodb.Client, cfg *config.Config) *services.Eraser {
	return services.NewEraser(
		repositories.NewCustomerRepository(db, cfg),
		repositories.NewSubscriptionRepository(db, cfg),
		repositories.NewInvoiceRepository(db, cfg),
		repositories.NewOrganizationRepository(db, cfg),
		repositories.NewCatalogRepository(db, cfg),
		repositories.NewLevelRepository(db, cfg),
		cfg.PortalOrganizationID,
		portalInvoicesURL(cfg.CheckoutBaseURL),
	)
}

// ErasedSpaceGuard is the guard the cmd/ jobs that write into a finance space
// from a work list read earlier (cmd/byok) chain with WithSpaceGuard. The API
// and BuildFinanceJobs wire the same services.ErasedSpace.
func ErasedSpaceGuard(ctx context.Context, cfg *config.Config) (func(context.Context, space.ResolvedSpace) (bool, error), error) {
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return services.ErasedSpace(newErasureStore(db, cfg)), nil
}

// erasureService is billing's participant id in the saga: the SNS filter
// value, the consumer's target check and the ack's service field.
const erasureService = "billing"

// erasureAckScope is what ctech-account's ack route requires.
const erasureAckScope = "internal:account:erasure-ack"

// exportAckScope is the extra scope the same client needs for the personal-data
// export. Until an operator grants it, ctech-account does not ask billing.
const exportAckScope = "internal:account:export-ack"

// erasureQueueName is terraform/billing/erasure.tf's queue. It is derived from
// ENVIRONMENT rather than configured, because the userdata is at its 16 KiB
// ceiling (terraform/README).
func erasureQueueName(env string) string { return env + "-ctech-billing-erasure" }

// erasureClientParams are terraform/billing/erasure.tf's SecureStrings for the
// dedicated ack client.
func erasureClientParams(env string) (id, secret string) {
	prefix := "/ctech-billing/" + env + "/billing/erasure-client-"
	return prefix + "id", prefix + "secret"
}

// usableErasureClient refuses an empty pair or Terraform's placeholder.
func usableErasureClient(id, secret string) error {
	if id == "" || secret == "" || id == "SET-OUT-OF-BAND" || secret == "SET-OUT-OF-BAND" {
		return errors.New("the erasure client is not set (ERASURE_CLIENT_ID/SECRET or its SSM parameters)")
	}
	return nil
}

// erasureCredentials returns the dedicated ack client: from the environment
// when set, otherwise from its two SSM SecureStrings.
func erasureCredentials(ctx context.Context, awsConf aws.Config, cfg *config.Config) (string, string, error) {
	if cfg.ErasureClientID != "" || cfg.ErasureClientSecret != "" {
		return cfg.ErasureClientID, cfg.ErasureClientSecret, usableErasureClient(cfg.ErasureClientID, cfg.ErasureClientSecret)
	}
	idName, secretName := erasureClientParams(cfg.Env)
	out, err := ssm.NewFromConfig(awsConf).GetParameters(ctx, &ssm.GetParametersInput{
		Names:          []string{idName, secretName},
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", "", fmt.Errorf("reading the erasure client from SSM: %w", err)
	}
	values := map[string]string{}
	for _, p := range out.Parameters {
		values[aws.ToString(p.Name)] = aws.ToString(p.Value)
	}
	id, secret := values[idName], values[secretName]
	return id, secret, usableErasureClient(id, secret)
}

// BuildErasureConsumer wires billing's side of the account-deletion saga: the
// SQS consumer that locks, purges and acks (api-commons/erasure).
//
// spaceFor is space.ForJob, passed in from cmd/server. A finance space is built
// from the saga message, which is an operator-grade decision, and ForJob is
// forbidden under internal/ (TestForJobIsNotCalledFromInternal).
//
// It returns (nil, nil) where there is nothing to consume for: no ctech-account
// URLs (a laptop, the integration tests), or a local DynamoDB. It returns an
// error, which the caller logs, for a missing queue or an unset erasure client.
func BuildErasureConsumer(ctx context.Context, cfg *config.Config, spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error), banks services.BankConnections) (*erasure.Consumer, error) {
	if cfg.AccountBaseURL == "" || cfg.AccountTokenURL == "" || cfg.DynamoDBEndpoint != "" {
		slog.Warn("account erasure consumer not started: no ctech-account URLs, or a local DynamoDB")
		return nil, nil
	}
	awsConf, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	clientID, clientSecret, err := erasureCredentials(ctx, awsConf, cfg)
	if err != nil {
		return nil, err
	}
	queues := sqs.NewFromConfig(awsConf)
	name := erasureQueueName(cfg.Env)
	q, err := queues.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return nil, fmt.Errorf("erasure queue %s: %w", name, err)
	}
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	// ctech-account: the ack and its token. A circuit per host, so a down account makes
	// the saga step fail at once and the message wait in the queue, not hold a goroutine.
	hc := resilient.Client(10*time.Second, resilient.Config{})
	acks := erasure.NewAckClient(hc,
		strings.TrimSuffix(cfg.AccountBaseURL, "/")+"/v1.0/internal/erasure/ack",
		oauth2client.New(hc, newCache(cfg), cfg.AccountTokenURL, clientID, clientSecret, erasureAckScope))
	eraser := newEraser(db, cfg).WithPurge(repositories.NewSpacePurger(db, cfg), spaceFor, banks)
	// The personal-data export rides the same queue (ctech-account
	// docs/specs/2026-10-17-data-export.md): the part is PUT to a presigned S3 URL
	// and acked. A longer timeout than the saga's, because the upload is data.
	uc := &http.Client{Timeout: 60 * time.Second}
	exporter := services.NewDataExporter(
		repositories.NewCustomerRepository(db, cfg), repositories.NewSubscriptionRepository(db, cfg),
		repositories.NewInvoiceRepository(db, cfg), repositories.NewSpaceReader(db, cfg),
		cfg.PortalOrganizationID, spaceFor)
	handler := dataexport.NewStreamHandler(erasureService, exporter.WritePart, uc, dataexport.NewAckClient(uc,
		strings.TrimSuffix(cfg.AccountBaseURL, "/")+"/v1.0/internal/export/ack",
		oauth2client.New(hc, newCache(cfg), cfg.AccountTokenURL, clientID, clientSecret, exportAckScope)))
	return erasure.NewConsumer(queues, aws.ToString(q.QueueUrl), erasureService,
		newErasureStore(db, cfg), services.AlertingPurge(eraser.Purge, jobs.Alerts(ctx, cfg)), acks).WithExporter(handler.Handle).
		WithVisibilityHeartbeat(cfg.ErasureQueueVisibility), nil
}

// newErasureStore is the one place the state table is opened, so the memory of
// its answers follows the same setting everywhere.
func newErasureStore(db *dynamodb.Client, cfg *config.Config) *erasure.Store {
	return erasure.NewStore(db, cfg.TablePrefix, 0).WithBlockedMemoryTTL(cfg.ErasureStateMemoryTTL)
}

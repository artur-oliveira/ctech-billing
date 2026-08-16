//go:build integration

// Package integration exercises the repository layer against a real DynamoDB.
//
// These tests exist because the interesting failures of this layer cannot be
// reproduced with a mock: a conditional write that does not actually fail, a
// transaction that is not actually atomic, an index whose key schema the service
// rejects. A fake that agrees with our assumptions proves only that we are
// consistent with ourselves.
//
// Run with: make test-integration (requires docker compose -f docker-compose.test.yml up -d)
package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var (
	testDB  *dynamodb.Client
	testCfg *config.Config
)

func TestMain(m *testing.M) {
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		fmt.Println("DYNAMODB_ENDPOINT not set — skipping integration tests")
		os.Exit(0)
	}

	// app.Build resolves credentials the normal way, so the fake ones DynamoDB
	// Local accepts have to be in the environment too.
	for k, v := range map[string]string{
		"AWS_ACCESS_KEY_ID":     "local",
		"AWS_SECRET_ACCESS_KEY": "local",
		"AWS_REGION":            "us-east-1",
	} {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}

	ctx := context.Background()
	awsConf, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
	)
	if err != nil {
		panic(err)
	}
	testDB = dynamodb.NewFromConfig(awsConf, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	// A unique prefix per run keeps repeated local runs from inheriting rows from
	// the previous one, which is how a test starts passing for the wrong reason.
	testCfg = &config.Config{
		TablePrefix: fmt.Sprintf("test%d", time.Now().UnixNano()),
		// Set explicitly because this is a struct literal, so no `envDefault` tag
		// runs. In production it comes from the release.env inside the artifact,
		// which is how the health report can name the build that answered it.
		AppVersion: "test",
		// A fixed key, because these tests assert on stored bytes. It is a test
		// key and it is in the repository on purpose — the production key comes
		// from SSM and this package never sees it.
		FieldEncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}

	if err := repositories.EnsureTables(ctx, testDB, testCfg.TablePrefix); err != nil {
		panic(fmt.Sprintf("create tables: %v", err))
	}

	os.Exit(m.Run())
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// envOrEmpty reads an environment variable, for tests that need the same
// endpoint the suite was pointed at.
func envOrEmpty(name string) string { return os.Getenv(name) }

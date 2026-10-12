// Command byok-webhook is the BYOK webhook Lambda (spec 2026-10-11 § 6.3):
// behind the mTLS custom domain, it parses a provider's callback into charge
// references and tells billing to re-query them. It holds no provider
// credential and no tenant secret: only its own M2M client, read from SSM.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/api-commons/oauth2client"
)

const notifyScope = "internal:billing:byok-notify"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("aws config", "error", err)
		os.Exit(1)
	}
	b := &billing{
		url:      strings.TrimRight(os.Getenv("BILLING_API_URL"), "/") + "/v1.0/internal/byok/notify",
		tokenURL: os.Getenv("CTECH_TOKEN_URL"),
		idParam:  os.Getenv("BYOK_WEBHOOK_CLIENT_PARAM"),
		param:    os.Getenv("BYOK_WEBHOOK_SECRET_PARAM"),
		ssm:      ssm.NewFromConfig(awsCfg),
		http:     &http.Client{Timeout: 8 * time.Second},
	}
	lambda.Start(newHandler(b.notify).handle)
}

// billing calls the notify route with the client's token. The secret is read
// on first use and kept for the container; a missing one (the client not
// created yet) is retried on the next call, and the provider retries a 503.
type billing struct {
	url, tokenURL, idParam, param string
	ssm                           *ssm.Client
	http                          *http.Client
	mu                            sync.Mutex
	tokens                        *oauth2client.TokenManager
}

func (b *billing) tokenManager(ctx context.Context) (*oauth2client.TokenManager, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens != nil {
		return b.tokens, nil
	}
	id, err := b.ssm.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(b.idParam)})
	if err != nil {
		return nil, fmt.Errorf("webhook client unreadable") // never the value or the SDK error body
	}
	out, err := b.ssm.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(b.param), WithDecryption: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("webhook client unreadable")
	}
	b.tokens = oauth2client.New(b.http, nil, b.tokenURL, aws.ToString(id.Parameter.Value), aws.ToString(out.Parameter.Value), notifyScope)
	return b.tokens, nil
}

func (b *billing) notify(ctx context.Context, in notifyRequest) (int, error) {
	tm, err := b.tokenManager(ctx)
	if err != nil {
		return 0, err
	}
	tok, err := tm.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("token")
	}
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("billing unreachable")
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

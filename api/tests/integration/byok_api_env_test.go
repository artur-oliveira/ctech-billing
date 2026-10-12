//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/byok/byoktest"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	mw "gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// byokScopes is a console session that may also read and write Finanças.
var byokScopes = append(append([]string{}, allConsoleScopes...), mw.ScopeFinanceRead, mw.ScopeFinanceWrite)

type byokAPI struct {
	*apiEnv
	fake      *byoktest.Fake
	byokCalls *atomic.Int64
	logs      *lockedBuffer
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// byokTableCalls counts every DynamoDB call that names the byok table, the
// zero-reads-before-membership proof (ADR 0025) applied to the connection rows.
func byokTableCalls(n *atomic.Int64) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("count-byok",
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
				var table string
				switch p := in.Parameters.(type) {
				case *dynamodb.GetItemInput:
					table = *p.TableName
				case *dynamodb.QueryInput:
					table = *p.TableName
				case *dynamodb.UpdateItemInput:
					table = *p.TableName
				case *dynamodb.PutItemInput:
					table = *p.TableName
				case *dynamodb.TransactWriteItemsInput:
					for _, it := range p.TransactItems {
						if it.Put != nil && strings.HasSuffix(*it.Put.TableName, "_byok") {
							table = *it.Put.TableName
						}
					}
				}
				if strings.HasSuffix(table, "_byok") {
					n.Add(1)
				}
				return next.HandleInitialize(ctx, in)
			}), middleware.Before)
	}
}

func newByokAPI(t *testing.T) *byokAPI {
	t.Helper()
	f := byoktest.New()
	calls := &atomic.Int64{}
	app.ByokInvoker = newFakeInvoker(f)
	app.DynamoDBAPIOptions = []func(*middleware.Stack) error{byokTableCalls(calls)}
	t.Cleanup(func() { app.ByokInvoker, app.DynamoDBAPIOptions = nil, nil })
	logs := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	e := newAPIWith(t, func(c *config.Config) { c.ByokOutboundFunction = "fake" })
	calls.Store(0)
	return &byokAPI{apiEnv: e, fake: f, byokCalls: calls, logs: logs}
}

func (e *byokAPI) capturedLogs() string { return e.logs.String() }

// byokWrite is consoleAs for a write: the Idempotency-Key the space
// idempotency middleware requires, fresh per call.
func (e *byokAPI) byokWrite(t *testing.T, method, path, token, mode, selector, body string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(mw.ModeHeader, mode)
	req.Header.Set(mw.SpaceHeader, selector)
	req.Header.Set(mw.IdempotencyHeader, "idem-"+id.New())
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: payload}
}

func (e *byokAPI) consoleAsUser(t *testing.T, user, method, path, mode, selector, body string) apiResponse {
	t.Helper()
	return e.byokWrite(t, method, path, e.token(t, user, "sess_"+id.New(), byokScopes...), mode, selector, body)
}

// testPair is a self-signed client certificate and its key, valid until notAfter.
func testPair(t *testing.T, notAfter time.Time) (certPEM, keyPEM string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "integration"},
		NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: notAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

// connectLive sends the live connect form with a generated certificate pair.
func (e *byokAPI) connectLive(t *testing.T, secret string) apiResponse {
	t.Helper()
	certPEM, keyPEM := testPair(t, time.Now().AddDate(1, 0, 0))
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range map[string]string{"client_id": "cid-123", "client_secret": secret, "account": "1234567",
		"pix_key": "evp-key", "notify_email": "financeiro@example.com", "backfill_months": "3", "authorized": "true"} {
		_ = w.WriteField(k, v)
	}
	for name, content := range map[string]string{"cert": certPEM, "key": keyPEM} {
		fw, _ := w.CreateFormFile(name, name+".pem")
		_, _ = io.WriteString(fw, content)
	}
	_ = w.Close()
	req := httptest.NewRequest("PUT", "/v1.0/console/connections/inter", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.sessionToken(t, byokScopes...))
	req.Header.Set(mw.ModeHeader, "live")
	req.Header.Set(mw.SpaceHeader, "org:"+e.accountOrg)
	req.Header.Set(mw.IdempotencyHeader, "idem-"+secret)
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: payload}
}

func (e *byokAPI) rawByokItem(t *testing.T, pk, sk string) map[string]types.AttributeValue {
	t.Helper()
	base := repositories.NewBase(testDB, testCfg, repositories.TableByok)
	item, err := base.GetItem(ctxT(t), pk, sk)
	if err != nil || item == nil {
		t.Fatalf("raw item %s %s: %v", pk, sk, err)
	}
	return item
}

// postSandboxLine puts one of this organization's charges on the shared
// sandbox statement, syncs it through the route and returns its transaction.
func (e *byokAPI) postSandboxLine(t *testing.T, amount int64) string {
	t.Helper()
	ref := "ctb" + strings.ToUpper(strings.ReplaceAll(e.accountOrg, "-", ""))[:26]
	base := repositories.NewBase(testDB, testCfg, repositories.TableByok)
	if err := base.PutItem(ctxT(t), map[string]types.AttributeValue{
		"pk":                      &types.AttributeValueMemberS{Value: repositories.ChargeRefPK("inter", false, ref)},
		"sk":                      &types.AttributeValueMemberS{Value: "CHARGEREF"},
		"account_organization_id": &types.AttributeValueMemberS{Value: e.accountOrg},
	}); err != nil {
		t.Fatal(err)
	}
	e.fake.AddLines(lineWithRef(brcal.FromTime(now()), "SB1", ref, amount))
	tok := e.sessionToken(t, byokScopes...)
	if r := e.byokWrite(t, "POST", "/v1.0/console/connections/inter/sync", tok, "test", "org:"+e.accountOrg, ""); r.status != 202 {
		t.Fatalf("sync: %d %s", r.status, r.body)
	}
	conn := e.consoleAs(t, "GET", "/v1.0/console/connections/inter", tok, "test", "org:"+e.accountOrg, "")
	var dto struct {
		LedgerAccountID string `json:"ledger_account_id"`
	}
	conn.decode(t, &dto)
	un := e.consoleAs(t, "GET", "/v1.0/console/finance/accounts/"+dto.LedgerAccountID+"/unclassified", tok, "test", "org:"+e.accountOrg, "")
	var list struct {
		Data []struct {
			TxID string `json:"tx_id"`
		} `json:"data"`
	}
	un.decode(t, &list)
	if un.status != 200 || len(list.Data) != 1 {
		t.Fatalf("unclassified: %d %s", un.status, un.body)
	}
	return list.Data[0].TxID
}

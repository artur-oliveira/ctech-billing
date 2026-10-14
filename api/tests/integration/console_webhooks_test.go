//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

const hooksPath = "/v1.0/console/webhooks"

type endpointBody struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Status      string   `json:"status"`
	Provisioned bool     `json:"provisioned"`
	Secret      string   `json:"secret"`
}

type endpointList struct {
	Data         []endpointBody `json:"data"`
	EventTypes   []string       `json:"event_types"`
	MaxEndpoints int            `json:"max_endpoints"`
}

func (e *apiEnv) addHook(t *testing.T, token, url string, events ...string) apiResponse {
	t.Helper()
	if events == nil {
		events = []string{}
	}
	body, _ := json.Marshal(map[string]any{"url": url, "events": events})
	return e.consoleWrite(t, http.MethodPost, hooksPath, token, "live", string(body))
}

// deliverTo sends requests for example.com to c and trusts c's certificate
// (httptest's certificate is valid for example.com). Other hosts are dialled
// as they are: the delivery queue is shared with every other test's tenants.
func deliverTo(t *testing.T, c *consumer) *services.Deliverer {
	t.Helper()
	tr := c.server.Client().Transport.(*http.Transport).Clone()
	addr := c.server.Listener.Addr().String()
	tr.DialTLSContext = nil
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "example.com:443" {
			address = addr
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	tr.TLSClientConfig = &tls.Config{RootCAs: tr.TLSClientConfig.RootCAs, ServerName: "example.com"}
	d := services.NewDeliverer(repositories.NewWebhookRepository(testDB, testCfg))
	d.SetHTTPClient(&http.Client{Timeout: 5 * time.Second, Transport: tr})
	return d
}

// Spec § 10.5 and Review Focus 5.
func TestTheSecretAppearsInExactlyOneResponsePerCreateOrRotate(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	res := e.addHook(t, tok, "https://example.com/hooks")
	if res.status != http.StatusCreated {
		t.Fatalf("create: %d %s", res.status, res.body)
	}
	var created endpointBody
	res.decode(t, &created)
	if len(created.Secret) != len("whsec_")+64 {
		t.Fatalf("secret = %q", created.Secret)
	}

	list := e.console(t, hooksPath, tok, "live")
	if bytes.Contains(list.body, []byte(created.Secret)) || bytes.Contains(list.body, []byte(`"secret"`)) {
		t.Fatalf("the list carries a secret: %s", list.body)
	}

	rot := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+created.ID+"/rotate-secret", tok, "live", "")
	var rotated endpointBody
	rot.decode(t, &rotated)
	if rot.status != http.StatusOK || rotated.Secret == "" || rotated.Secret == created.Secret {
		t.Fatalf("rotate: %d %+v", rot.status, rotated)
	}
	list = e.console(t, hooksPath, tok, "live")
	if bytes.Contains(list.body, []byte(rotated.Secret)) || bytes.Contains(list.body, []byte(created.Secret)) {
		t.Fatalf("the list after rotate carries a secret: %s", list.body)
	}

	// Sealed at rest.
	item, err := testDB.GetItem(ctxT(t), &dynamodb.GetItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableWebhooks)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(e.org.ID, true)},
			"sk": &types.AttributeValueMemberS{Value: repositories.EndpointSK(created.ID)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored := item.Item["secret"].(*types.AttributeValueMemberS).Value; stored == rotated.Secret {
		t.Fatal("the secret is stored in the clear")
	}

	del := e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+created.ID, tok, "live", "")
	if del.status != http.StatusNoContent || len(del.body) != 0 {
		t.Fatalf("delete: %d %s", del.status, del.body)
	}
}

func TestWebhookURLsMustBeHTTPSAndPublic(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	// Their own field codes (final review I3): the generic ones read
	// "Use: {{allowed}}" with nothing to fill in.
	for url, code := range map[string]string{
		"http://example.com/hooks":     "webhook_url_not_https",
		"not a url":                    "webhook_url_not_https",
		"https://127.0.0.1/hooks":      "webhook_host_not_public",
		"https://localhost/hooks":      "webhook_host_not_public",
		"https://billing.internal/x":   "webhook_host_not_public",
		"https://169.254.169.254/meta": "webhook_host_not_public",
	} {
		res := e.addHook(t, tok, url)
		var p struct {
			Code   string `json:"code"`
			Errors []struct {
				Field, Code string
			} `json:"errors"`
		}
		res.decode(t, &p)
		if res.status != 422 || len(p.Errors) != 1 || p.Errors[0].Field != "url" || p.Errors[0].Code != code {
			t.Errorf("%s: %d %s", url, res.status, res.body)
		}
	}
}

func TestOnlyListedEventsAreAccepted(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	for _, events := range [][]string{{"invoice.paid", "nope.never"}, {"ping"}} {
		if res := e.addHook(t, tok, "https://example.com/hooks", events...); res.status != 422 {
			t.Errorf("%v: %d %s", events, res.status, res.body)
		}
	}
	var list endpointList
	e.console(t, hooksPath, tok, "live").decode(t, &list)
	if list.MaxEndpoints != 5 || len(list.EventTypes) != len(billing.WebhookEventTypes()) {
		t.Fatalf("list meta = %d %v", list.MaxEndpoints, list.EventTypes)
	}
}

func TestTheSixthEndpointIsRefused(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var first endpointBody
	for i := 0; i < 5; i++ {
		res := e.addHook(t, tok, fmt.Sprintf("https://example.com/hooks/%d", i))
		if res.status != http.StatusCreated {
			t.Fatalf("create %d: %d %s", i, res.status, res.body)
		}
		if i == 0 {
			res.decode(t, &first)
		}
	}
	res := e.addHook(t, tok, "https://example.com/hooks/6")
	var p problemBody
	res.decode(t, &p)
	if res.status != 422 || p.Code != "webhook_endpoint_limit" {
		t.Fatalf("sixth: %d %+v", res.status, p)
	}
	e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+first.ID, tok, "live", "")
	if res := e.addHook(t, tok, "https://example.com/hooks/7"); res.status != http.StatusCreated {
		t.Fatalf("after a delete: %d", res.status)
	}
}

// The test call is a ping through the normal fan-out and delivery, signed with
// the endpoint's secret, and only that endpoint receives it.
func TestTheTestCallSendsAPingThroughNormalDelivery(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	c := newConsumer(t, http.StatusOK)

	var target, other endpointBody
	e.addHook(t, tok, "https://example.com/target", "invoice.paid").decode(t, &target)
	e.addHook(t, tok, "https://example.com/other").decode(t, &other)

	res := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+target.ID+"/test", tok, "live", "")
	var accepted struct {
		EventID string `json:"event_id"`
	}
	res.decode(t, &accepted)
	if res.status != http.StatusAccepted || accepted.EventID == "" {
		t.Fatalf("test: %d %s", res.status, res.body)
	}

	runDelivery(t, deliverTo(t, c), e.org, now())
	// Both endpoints point at c; the ping must arrive once (the target), never twice.
	var pings []received
	for _, d := range c.deliveries() {
		if d.payload.ID == accepted.EventID {
			pings = append(pings, d)
		}
	}
	if len(pings) != 1 {
		t.Fatalf("the ping arrived %d times, want 1 (the target only)", len(pings))
	}
	d := pings[0]
	if d.payload.Type != billing.EventPing ||
		d.payload.Data.Object != "webhook_endpoint" || d.payload.Data.ID != target.ID {
		t.Fatalf("payload = %+v", d.payload)
	}
	if d.signature != "v1="+services.Sign(target.Secret, d.timestamp, d.body) {
		t.Fatal("the ping is not signed with the endpoint's secret")
	}
}

func TestProvisionedEndpointsAreReadOnlyInTheConsole(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	seeded := registerEndpoint(t, e.org, newConsumer(t, http.StatusOK), "dfe")
	var list endpointList
	e.console(t, hooksPath, tok, "live").decode(t, &list)
	if len(list.Data) != 1 || !list.Data[0].Provisioned {
		t.Fatalf("list = %+v", list.Data)
	}
	for _, path := range []string{"/" + seeded.ID + "/rotate-secret", "/" + seeded.ID + "/test"} {
		res := e.consoleWrite(t, http.MethodPost, hooksPath+path, tok, "live", "")
		var p problemBody
		res.decode(t, &p)
		if res.status != http.StatusConflict || p.Code != "webhook_endpoint_provisioned" {
			t.Errorf("%s: %d %+v", path, res.status, p)
		}
	}
	res := e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+seeded.ID, tok, "live", "")
	if res.status != http.StatusConflict {
		t.Errorf("delete: %d", res.status)
	}
}

func TestADisabledEndpointRefusesTheTest(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var ep endpointBody
	e.addHook(t, tok, "https://example.com/hooks").decode(t, &ep)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	full, err := repo.GetEndpoint(ctxT(t), e.org.ID, true, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	full.FailureRun = billing.MaxFailureRun - 1
	if err := repo.RecordEndpointHealth(ctxT(t), full, false, "HTTP 500", now()); err != nil {
		t.Fatal(err)
	}
	res := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/test", tok, "live", "")
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusConflict || p.Code != "webhook_endpoint_disabled" {
		t.Fatalf("%d %+v", res.status, p)
	}
}

// Decision 1 for webhooks: delete and rotate yes; create and test no.
func TestASuspendedTenantCanOnlyShrinkOrSecureItsWebhooks(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var ep endpointBody
	e.addHook(t, tok, "https://example.com/hooks").decode(t, &ep)
	suspendLive(t, e.org)

	for _, res := range []apiResponse{
		e.addHook(t, tok, "https://example.com/other"),
		e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/test", tok, "live", ""),
	} {
		var p problemBody
		res.decode(t, &p)
		if res.status != http.StatusForbidden || p.Code != "tenant_suspended" {
			t.Errorf("%d %+v", res.status, p)
		}
	}
	if r := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/rotate-secret", tok, "live", ""); r.status != http.StatusOK {
		t.Errorf("rotate while suspended: %d", r.status)
	}
	if r := e.consoleWrite(t, http.MethodDelete, hooksPath+"/"+ep.ID, tok, "live", ""); r.status != http.StatusNoContent {
		t.Errorf("delete while suspended: %d", r.status)
	}
}

func TestConcurrentConsoleCreatesStayWithinTheLimit(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := e.addHook(t, tok, fmt.Sprintf("https://example.com/c/%d", i))
			if res.status != http.StatusCreated && res.status != 422 && res.status != http.StatusConflict {
				t.Errorf("%d %s", res.status, res.body)
			}
		}()
	}
	wg.Wait()
	var list endpointList
	e.console(t, hooksPath, tok, "live").decode(t, &list)
	if len(list.Data) > 5 {
		t.Fatalf("%d endpoints", len(list.Data))
	}
}

// Final review I1: the test call queues an event in the queue every tenant
// shares, so it is refused while the last one is recent.
func TestTheTestCallIsRefusedWhileTheLastOneIsRecent(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var ep endpointBody
	e.addHook(t, tok, "https://hooks.invalid/one").decode(t, &ep)
	if r := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/test", tok, "live", ""); r.status != http.StatusAccepted {
		t.Fatalf("first test: %d %s", r.status, r.body)
	}
	res := e.consoleWrite(t, http.MethodPost, hooksPath+"/"+ep.ID+"/test", tok, "live", "")
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusTooManyRequests || p.Code != "webhook_test_too_soon" {
		t.Fatalf("second test: %d %+v", res.status, p)
	}
}

// Registering an endpoint needs it to answer the challenge: a URL that does
// not is refused and nothing is stored (so it does not use up a slot).
func TestAWebhookEndpointMustAnswerItsChallengeToBeRegistered(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)

	var asked string
	e.verifier.fn = func(url string) error {
		asked = url
		return errors.New("the endpoint did not return the hub.challenge value")
	}
	res := e.addHook(t, tok, "https://example.com/not-ours")
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusUnprocessableEntity || p.Code != "webhook_verification_failed" {
		t.Fatalf("%d %s", res.status, res.body)
	}
	if asked != "https://example.com/not-ours" {
		t.Fatalf("verified %q", asked)
	}
	list := e.console(t, hooksPath, tok, "live")
	var page struct {
		Data []endpointBody `json:"data"`
	}
	list.decode(t, &page)
	for _, ep := range page.Data {
		if ep.URL == "https://example.com/not-ours" {
			t.Fatal("a refused endpoint was stored")
		}
	}

	e.verifier.fn = nil
	if res := e.addHook(t, tok, "https://example.com/ours"); res.status != http.StatusCreated {
		t.Fatalf("after confirming: %d %s", res.status, res.body)
	}
}

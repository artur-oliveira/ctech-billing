package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type fakeBilling struct {
	calls  []notifyRequest
	status int
}

func (f *fakeBilling) notify(_ context.Context, in notifyRequest) (int, error) {
	f.calls = append(f.calls, in)
	return f.status, nil
}

const secret = "s3cr3t-path-secret-value-abcdefghijklmnopq"

func call(t *testing.T, h *handler, path, body string) events.APIGatewayV2HTTPResponse {
	t.Helper()
	resp, err := h.handle(context.Background(), events.APIGatewayV2HTTPRequest{RawPath: path, Body: body,
		Headers: map[string]string{"x-conta-corrente": "1234567"}})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

const pixBody = `{"pix":[{"txid":"ctb01JABCDEFGHJKMNPQRSTVWXYZ","endToEndId":"E00000000202603101200abcdefghijk","valor":"50.00"}]}`

func TestBothPathVariantsReachBillingWithTheSameBody(t *testing.T) {
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	b := &fakeBilling{status: 200}
	h := newHandler(b.notify)
	for _, p := range []string{"/w/inter/01HOOK/" + secret + "/pix", "/w/inter/01HOOK/" + secret + "/pix/pix"} {
		if r := call(t, h, p, pixBody); r.StatusCode != 200 {
			t.Fatalf("%s: %d", p, r.StatusCode)
		}
	}
	if len(b.calls) != 2 {
		t.Fatal(len(b.calls))
	}
	a, _ := json.Marshal(b.calls[0])
	c, _ := json.Marshal(b.calls[1])
	if string(a) != string(c) || b.calls[0].HookID != "01HOOK" || b.calls[0].Secret != secret || b.calls[0].Account != "1234567" ||
		len(b.calls[0].ChargeRefs) != 1 || b.calls[0].Product != "pix" || b.calls[0].Provider != "inter" {
		t.Fatalf("%s", a)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatal("the hook's secret never reaches the logs")
	}
	if !strings.Contains(logs.String(), "50.00") || !strings.Contains(logs.String(), "E00000000202603101200abcdefghijk") {
		t.Fatal("the body Inter sent is kept in full: we have to know what it carries")
	}
}

func TestOtherPathsAre404WithoutABillingCall(t *testing.T) {
	b := &fakeBilling{status: 200}
	h := newHandler(b.notify)
	for _, p := range []string{"/w/inter/01HOOK/" + secret + "/rec", "/w/inter/01HOOK/" + secret + "/pix/rec",
		"/w/inter/01HOOK/" + secret + "/pix/pix/pix", "/w/other/01HOOK/" + secret + "/pix", "/x/inter/01HOOK/" + secret + "/pix", "/w/inter/01HOOK"} {
		if r := call(t, h, p, pixBody); r.StatusCode != 404 {
			t.Fatalf("%s: %d", p, r.StatusCode)
		}
	}
	if len(b.calls) != 0 {
		t.Fatal("billing was called")
	}
}

func TestBillingsAnswerPassesThroughAndABadBodyIs400(t *testing.T) {
	for _, st := range []int{200, 404, 503} {
		b := &fakeBilling{status: st}
		if r := call(t, newHandler(b.notify), "/w/inter/01HOOK/"+secret+"/pix", pixBody); r.StatusCode != st {
			t.Fatalf("%d → %d", st, r.StatusCode)
		}
	}
	b := &fakeBilling{status: 401}
	if r := call(t, newHandler(b.notify), "/w/inter/01HOOK/"+secret+"/pix", pixBody); r.StatusCode != 503 {
		t.Fatalf("a token problem is retried by the provider: %d", r.StatusCode)
	}
	if r := call(t, newHandler(b.notify), "/w/inter/01HOOK/"+secret+"/pix", "not json"); r.StatusCode != 400 {
		t.Fatalf("%d", r.StatusCode)
	}
}

func TestTheRequestIsLoggedWholeWithItsHeadersButNoCredentials(t *testing.T) {
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	b := &fakeBilling{status: 200}
	_, err := newHandler(b.notify).handle(context.Background(), events.APIGatewayV2HTTPRequest{
		RawPath: "/w/inter/01HOOK/" + secret + "/pix", Body: pixBody,
		Headers:        map[string]string{"x-conta-corrente": "1234567", "Authorization": "Bearer abc", "user-agent": "inter"},
		RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST", SourceIP: "198.51.100.7"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	for _, want := range []string{"inter webhook request", "inter webhook response", "198.51.100.7", "x-conta-corrente", "1234567", "user-agent", `"method":"POST"`, "/w/inter/01HOOK/<redacted>/pix"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Bearer abc") || strings.Contains(out, secret) {
		t.Fatal("a credential reached the logs")
	}
}

func TestEvenARequestThatIsRefusedIsLogged(t *testing.T) {
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	h := newHandler((&fakeBilling{status: 200}).notify)
	if r := call(t, h, "/x/inter/01HOOK/"+secret+"/pix", `{"pix":[{"txid":"scan"}]}`); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if !strings.Contains(logs.String(), `{\"pix\":[{\"txid\":\"scan\"}]}`) || strings.Contains(logs.String(), secret) {
		t.Fatalf("a refused request is evidence too, without the secret:\n%s", logs.String())
	}
}

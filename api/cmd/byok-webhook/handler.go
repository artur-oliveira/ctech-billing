package main

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/inter"
)

// notifyRequest is billing's POST /v1.0/internal/byok/notify body (spec
// § 6.3). It names references only: what to re-query, never what was paid.
type notifyRequest struct {
	Provider   string   `json:"provider"`
	HookID     string   `json:"hook_id"`
	Secret     string   `json:"secret"`
	Product    string   `json:"product"`
	Account    string   `json:"account"`
	ChargeRefs []string `json:"charge_refs"`
	PaymentIDs []string `json:"payment_ids"`
	// Payers is who the body names as payer (name, masked document): a claim that
	// billing uses as provisional evidence after it has re-queried the charge.
	Payers []byok.CallbackPayer `json:"payers,omitempty"`
}

type handler struct {
	parsers map[string]byok.Provider
	notify  func(context.Context, notifyRequest) (int, error)
}

func newHandler(notify func(context.Context, notifyRequest) (int, error)) *handler {
	// Parsing needs no client and no credential: this Lambda holds none.
	return &handler{parsers: map[string]byok.Provider{"inter": inter.NewProvider(nil)}, notify: notify}
}

// splitPath reads /w/{provider}/{hook}/{secret}/{product}, also with the
// product repeated once (Inter documents appending /pix; spec § 6.2). The
// parser is chosen by the provider and product, never by the suffix.
func splitPath(p string) (provider, hook, secret string, product byok.Product, ok bool) {
	seg := strings.Split(strings.Trim(p, "/"), "/")
	if len(seg) < 5 || len(seg) > 6 || seg[0] != "w" {
		return
	}
	if len(seg) == 6 && seg[5] != seg[4] {
		return
	}
	if seg[1] != "inter" || seg[4] != string(byok.ProductPix) || seg[2] == "" || seg[3] == "" {
		return
	}
	return seg[1], seg[2], seg[3], byok.Product(seg[4]), true
}

func status(code int) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{StatusCode: code}
}

// maxLoggedBody keeps one log event under CloudWatch's 256 KiB; a Pix webhook is
// a few hundred bytes, so this only ever trims abuse.
const maxLoggedBody = 200 << 10

// redactedHeaders are credentials, not evidence. Everything else Inter sends is
// logged as it came.
var redactedHeaders = map[string]bool{"authorization": true, "cookie": true, "x-api-key": true}

// redactPath hides the hook's secret, which is a path segment: every segment
// long enough to be one (the hook id is shorter) is replaced.
func redactPath(p string) string {
	seg := strings.Split(p, "/")
	for i, s := range seg {
		if len(s) >= 30 {
			seg[i] = "<redacted>"
		}
	}
	return strings.Join(seg, "/")
}

// logRequest records exactly what arrived: the method, the path without its
// secret, the source, every header but credentials, and the whole body. We have
// to know what Inter really sends (whether it names the payer, and how), and the
// only way to know is to keep it.
func logRequest(ctx context.Context, req events.APIGatewayV2HTTPRequest, body []byte) {
	headers := make(map[string]string, len(req.Headers))
	for k, v := range req.Headers {
		if redactedHeaders[strings.ToLower(k)] {
			v = "<redacted>"
		}
		headers[k] = v
	}
	shown, truncated := body, false
	if len(shown) > maxLoggedBody {
		shown, truncated = shown[:maxLoggedBody], true
	}
	slog.InfoContext(ctx, "inter webhook request",
		"method", req.RequestContext.HTTP.Method, "path", redactPath(req.RawPath), "query", req.RawQueryString != "",
		"source_ip", req.RequestContext.HTTP.SourceIP, "headers", headers,
		"body_bytes", len(body), "body", string(shown), "body_truncated", truncated)
}

func (h *handler) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		if b, err := base64.StdEncoding.DecodeString(req.Body); err == nil {
			body = b
		}
	}
	logRequest(ctx, req, body)
	resp, err := h.serve(ctx, req)
	slog.InfoContext(ctx, "inter webhook response", "status", resp.StatusCode)
	return resp, err
}

func (h *handler) serve(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	provider, hook, secret, product, ok := splitPath(req.RawPath)
	if !ok {
		return status(http.StatusNotFound), nil
	}
	body := []byte(req.Body)
	if req.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return status(http.StatusBadRequest), nil
		}
		body = b
	}
	headers := make(map[string]string, len(req.Headers))
	for k, v := range req.Headers {
		headers[strings.ToLower(k)] = v
	}
	cb, err := h.parsers[provider].ParseCallback(product, headers, body)
	if err != nil {
		slog.WarnContext(ctx, "byok webhook: unparseable body", "hook_id", hook)
		return status(http.StatusBadRequest), nil
	}
	code, err := h.notify(ctx, notifyRequest{Provider: provider, HookID: hook, Secret: secret, Product: string(product),
		Account: cb.Account, ChargeRefs: cb.ChargeRefs, PaymentIDs: cb.PaymentIDs, Payers: cb.Payers})
	if err != nil {
		slog.WarnContext(ctx, "byok webhook: billing unreachable", "hook_id", hook)
		return status(http.StatusServiceUnavailable), nil
	}
	slog.InfoContext(ctx, "byok webhook", "hook_id", hook, "refs", len(cb.ChargeRefs), "billing_status", code)
	switch code {
	case http.StatusOK, http.StatusNotFound, http.StatusServiceUnavailable:
		return status(code), nil
	case http.StatusBadRequest:
		return status(http.StatusBadRequest), nil
	}
	// A token or configuration problem on CTech's side: the provider retries.
	return status(http.StatusServiceUnavailable), nil
}

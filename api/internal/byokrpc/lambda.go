// Package byokrpc is the api's side of the outbound Lambda: a byok.Provider
// over lambda:InvokeFunction, the token lifecycle (spec § 4.3) and the
// limiters. Billing's services depend on byok.Provider and on this package,
// never on api-commons/inter.
package byokrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api-commons/byok"
)

type Invoker interface {
	Invoke(ctx context.Context, in *lambda.InvokeInput, opts ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

type Outbound struct {
	inv      Invoker
	function string
}

func NewOutbound(inv Invoker, function string) *Outbound {
	return &Outbound{inv: inv, function: function}
}

// For binds the provider and the mode (the AAD and the gate need both).
func (o *Outbound) For(provider string, livemode bool) *LambdaProvider {
	mode := "test"
	if livemode {
		mode = "live"
	}
	return &LambdaProvider{o: o, provider: provider, mode: mode}
}

type LambdaProvider struct {
	o        *Outbound
	provider string
	mode     string
}

func ref(c byok.Credentials) byok.ConnectionRef {
	if c.Ref == nil {
		return byok.ConnectionRef{}
	}
	return *c.Ref
}

func invoke[T any](ctx context.Context, p *LambdaProvider, op byok.Op, conn byok.ConnectionRef, bearer string, args any) (T, error) {
	var zero T
	payload, err := json.Marshal(args)
	if err != nil {
		return zero, err
	}
	body, err := json.Marshal(byok.Request{Op: op, Provider: p.provider, Mode: p.mode, Connection: conn, OAuthToken: bearer, Payload: payload})
	if err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	out, err := p.o.inv.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String(p.o.function), Payload: body})
	if err != nil {
		return zero, fmt.Errorf("%w: outbound Lambda unreachable", byok.ErrUnavailable)
	}
	if out.FunctionError != nil {
		return zero, fmt.Errorf("%w: outbound Lambda failed", byok.ErrUnavailable)
	}
	var resp byok.Response
	if err := json.Unmarshal(out.Payload, &resp); err != nil {
		return zero, fmt.Errorf("%w: outbound response", byok.ErrUnavailable)
	}
	if resp.Code != "" {
		return zero, byok.ErrorFromCode(resp.Code, resp.Error)
	}
	var v T
	if len(resp.Payload) > 0 {
		if err := json.Unmarshal(resp.Payload, &v); err != nil {
			return zero, fmt.Errorf("%w: outbound payload", byok.ErrUnavailable)
		}
	}
	return v, nil
}

type none struct{}

func (p *LambdaProvider) Name() string                   { return p.provider }
func (p *LambdaProvider) CredentialFields() []byok.Field { return nil } // the console's fields are per provider (Task 19)

// Connect sends plaintext once; the Lambda tests, seals and returns Sealed.
func (p *LambdaProvider) Connect(ctx context.Context, c byok.Credentials) (byok.Connected, error) {
	return invoke[byok.Connected](ctx, p, byok.OpConnect, ref(c), "", byok.ConnectArgs{Values: c.Values, Account: c.Account, PixKey: c.PixKey})
}

func (p *LambdaProvider) Reseal(ctx context.Context, r byok.ConnectionRef, certPEM, keyPEM string) (byok.Connected, error) {
	return invoke[byok.Connected](ctx, p, byok.OpReseal, r, "", byok.ResealArgs{CertPEM: certPEM, KeyPEM: keyPEM})
}

func (p *LambdaProvider) Token(ctx context.Context, c byok.Credentials, set byok.Capability) (byok.Token, error) {
	return invoke[byok.Token](ctx, p, byok.OpToken, ref(c), "", byok.TokenArgs{Set: set})
}

func (p *LambdaProvider) CreateDueCharge(ctx context.Context, c byok.Credentials, t byok.Token, in byok.DueCharge) (byok.Charge, error) {
	return invoke[byok.Charge](ctx, p, byok.OpCreateDueCharge, ref(c), t.Value, in)
}

func (p *LambdaProvider) GetCharge(ctx context.Context, c byok.Credentials, t byok.Token, r string) (byok.Charge, error) {
	return invoke[byok.Charge](ctx, p, byok.OpGetCharge, ref(c), t.Value, byok.RefArgs{Ref: r})
}

func (p *LambdaProvider) UpdateCharge(ctx context.Context, c byok.Credentials, t byok.Token, r string, amount int64) (byok.Charge, error) {
	return invoke[byok.Charge](ctx, p, byok.OpUpdateCharge, ref(c), t.Value, byok.UpdateArgs{Ref: r, Amount: amount})
}

func (p *LambdaProvider) CancelCharge(ctx context.Context, c byok.Credentials, t byok.Token, r string) error {
	_, err := invoke[none](ctx, p, byok.OpCancelCharge, ref(c), t.Value, byok.RefArgs{Ref: r})
	return err
}

func (p *LambdaProvider) Refund(ctx context.Context, c byok.Credentials, t byok.Token, in byok.RefundRequest) (byok.Refund, error) {
	return invoke[byok.Refund](ctx, p, byok.OpRefund, ref(c), t.Value, in)
}

func (p *LambdaProvider) GetRefund(ctx context.Context, c byok.Credentials, t byok.Token, paymentID, refundID string) (byok.Refund, error) {
	return invoke[byok.Refund](ctx, p, byok.OpGetRefund, ref(c), t.Value, byok.GetRefundArgs{PaymentID: paymentID, RefundID: refundID})
}

func (p *LambdaProvider) StatementPage(ctx context.Context, c byok.Credentials, t byok.Token, from, to byok.Date, page int) (byok.StatementPage, error) {
	return invoke[byok.StatementPage](ctx, p, byok.OpStatementPage, ref(c), t.Value, byok.StatementArgs{From: from, To: to, Page: page})
}

func (p *LambdaProvider) Balance(ctx context.Context, c byok.Credentials, t byok.Token, at byok.Date) (byok.Balance, error) {
	return invoke[byok.Balance](ctx, p, byok.OpBalance, ref(c), t.Value, byok.BalanceArgs{At: at})
}

func (p *LambdaProvider) GetWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product) (string, error) {
	r, err := invoke[byok.WebhookResult](ctx, p, byok.OpGetWebhook, ref(c), t.Value, byok.WebhookArgs{Product: product})
	return r.URL, err
}

func (p *LambdaProvider) RegisterWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product, url string) error {
	_, err := invoke[none](ctx, p, byok.OpRegisterWebhook, ref(c), t.Value, byok.WebhookArgs{Product: product, URL: url})
	return err
}

func (p *LambdaProvider) DeleteWebhook(ctx context.Context, c byok.Credentials, t byok.Token, product byok.Product) error {
	_, err := invoke[none](ctx, p, byok.OpDeleteWebhook, ref(c), t.Value, byok.WebhookArgs{Product: product})
	return err
}

func (p *LambdaProvider) ListCallbacks(ctx context.Context, c byok.Credentials, t byok.Token, since time.Time) ([]byok.CallbackAttempt, error) {
	return invoke[[]byok.CallbackAttempt](ctx, p, byok.OpListCallbacks, ref(c), t.Value, byok.CallbacksArgs{Since: since})
}

func (p *LambdaProvider) RetryCallbacks(ctx context.Context, c byok.Credentials, t byok.Token, refs []string) error {
	_, err := invoke[none](ctx, p, byok.OpRetryCallbacks, ref(c), t.Value, byok.RetryArgs{Refs: refs})
	return err
}

func (p *LambdaProvider) SimulatePayment(ctx context.Context, c byok.Credentials, t byok.Token, r string) error {
	_, err := invoke[none](ctx, p, byok.OpSimulatePayment, ref(c), t.Value, byok.RefArgs{Ref: r})
	return err
}

// ParseCallback never runs in the api (the webhook Lambda parses).
func (p *LambdaProvider) ParseCallback(byok.Product, map[string]string, []byte) (byok.Callback, error) {
	return byok.Callback{}, byok.ErrInvalidRequest
}

var _ byok.Provider = (*LambdaProvider)(nil)

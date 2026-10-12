//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"
)

// fakeInvoker stands in for the outbound Lambda: it decodes the envelope and
// runs the op on a byoktest.Fake, answering with the Lambda's response shape.
// It holds the fake behind an atomic pointer so a test can swap in a shared one
// (two test organizations on one sandbox).
type fakeInvoker struct{ fake atomic.Pointer[byoktest.Fake] }

func newFakeInvoker(f *byoktest.Fake) *fakeInvoker {
	inv := &fakeInvoker{}
	inv.fake.Store(f)
	return inv
}

func decodeArgs[T any](raw json.RawMessage) T {
	var v T
	_ = json.Unmarshal(raw, &v)
	return v
}

func (i *fakeInvoker) Invoke(ctx context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	f := i.fake.Load()
	var req byok.Request
	_ = json.Unmarshal(in.Payload, &req)
	creds := byok.Credentials{Sandbox: req.Connection.ID == "sandbox"}
	tok := byok.Token{Value: req.OAuthToken}
	var out any
	var err error
	switch req.Op {
	case byok.OpConnect:
		a := decodeArgs[byok.ConnectArgs](req.Payload)
		var c byok.Connected
		c, err = f.Connect(ctx, byok.Credentials{Values: a.Values})
		c.Sealed = "b1.fake-" + req.Connection.ID
		out = c
	case byok.OpReseal:
		var c byok.Connected
		c, err = f.Connect(ctx, creds)
		c.Sealed = "b1.fake-resealed-" + req.Connection.ID
		out = c
	case byok.OpToken:
		out, err = f.Token(ctx, creds, decodeArgs[byok.TokenArgs](req.Payload).Set)
	case byok.OpBalance:
		out, err = f.Balance(ctx, creds, tok, decodeArgs[byok.BalanceArgs](req.Payload).At)
	case byok.OpStatementPage:
		a := decodeArgs[byok.StatementArgs](req.Payload)
		out, err = f.StatementPage(ctx, creds, tok, a.From, a.To, a.Page)
	case byok.OpGetCharge:
		out, err = f.GetCharge(ctx, creds, tok, decodeArgs[byok.RefArgs](req.Payload).Ref)
	case byok.OpGetWebhook:
		var u string
		u, err = f.GetWebhook(ctx, creds, tok, decodeArgs[byok.WebhookArgs](req.Payload).Product)
		out = byok.WebhookResult{URL: u}
	case byok.OpDeleteWebhook:
		err = f.DeleteWebhook(ctx, creds, tok, decodeArgs[byok.WebhookArgs](req.Payload).Product)
		out = struct{}{}
	case byok.OpCreateDueCharge:
		out, err = f.CreateDueCharge(ctx, creds, tok, decodeArgs[byok.DueCharge](req.Payload))
	case byok.OpUpdateCharge:
		a := decodeArgs[byok.UpdateArgs](req.Payload)
		out, err = f.UpdateCharge(ctx, creds, tok, a.Ref, a.Amount)
	case byok.OpCancelCharge:
		err = f.CancelCharge(ctx, creds, tok, decodeArgs[byok.RefArgs](req.Payload).Ref)
		out = struct{}{}
	case byok.OpRefund:
		out, err = f.Refund(ctx, creds, tok, decodeArgs[byok.RefundRequest](req.Payload))
	case byok.OpGetRefund:
		a := decodeArgs[byok.GetRefundArgs](req.Payload)
		out, err = f.GetRefund(ctx, creds, tok, a.PaymentID, a.RefundID)
	case byok.OpRegisterWebhook:
		a := decodeArgs[byok.WebhookArgs](req.Payload)
		err = f.RegisterWebhook(ctx, creds, tok, a.Product, a.URL)
		out = struct{}{}
	case byok.OpListCallbacks:
		out, err = f.ListCallbacks(ctx, creds, tok, decodeArgs[byok.CallbacksArgs](req.Payload).Since)
	case byok.OpRetryCallbacks:
		err = f.RetryCallbacks(ctx, creds, tok, decodeArgs[byok.RetryArgs](req.Payload).Refs)
		out = struct{}{}
	case byok.OpSimulatePayment:
		// The Lambda refuses SimulatePayment outside a sandbox (part A).
		if req.Mode != "test" {
			err = byok.ErrSandboxOnly
			break
		}
		err = f.SimulatePayment(ctx, creds, tok, decodeArgs[byok.RefArgs](req.Payload).Ref)
		out = struct{}{}
	default:
		err = byok.ErrInvalidRequest
	}
	var resp byok.Response
	if err != nil {
		resp = byok.Response{Code: byok.ErrorCode(err), Error: byok.ErrorCode(err)}
	} else {
		resp.Payload, _ = json.Marshal(out)
	}
	b, _ := json.Marshal(resp)
	return &lambda.InvokeOutput{StatusCode: 200, Payload: b}, nil
}

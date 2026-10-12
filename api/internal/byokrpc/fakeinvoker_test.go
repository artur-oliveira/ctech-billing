package byokrpc

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"
)

type fakeInvoker struct {
	fake         *byoktest.Fake
	tokens       atomic.Int32
	unauthorized atomic.Int32 // answer this many Balance calls with unauthorized
	last         atomic.Value // last byok.Request
}

func (f *fakeInvoker) Invoke(ctx context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	var req byok.Request
	_ = json.Unmarshal(in.Payload, &req)
	f.last.Store(req)
	var resp byok.Response
	switch req.Op {
	case byok.OpToken:
		f.tokens.Add(1)
		resp.Payload, _ = json.Marshal(byok.Token{Value: "t", ExpiresIn: 3600})
	case byok.OpBalance:
		if f.unauthorized.Load() > 0 {
			f.unauthorized.Add(-1)
			resp = byok.Response{Code: "unauthorized", Error: "unauthorized"}
			break
		}
		b, _ := f.fake.Balance(ctx, byok.Credentials{}, byok.Token{}, byok.Date{})
		resp.Payload, _ = json.Marshal(b)
	default:
		resp = byok.Response{Code: "invalid_request", Error: "unsupported in this fake"}
	}
	out, _ := json.Marshal(resp)
	return &lambda.InvokeOutput{StatusCode: 200, Payload: out}, nil
}

package byokrpc

import (
	"context"
	"errors"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

// Session is one connection's calls: provider bound to the mode, tokens, limits.
type Session struct {
	P      *LambdaProvider
	Conn   *byokconn.Connection
	Tokens *TokenManager
	Limits *Limits
}

func (s Session) Credentials() byok.Credentials { return byok.Credentials{Ref: refPtr(s.Conn)} }

// Call runs fn with a bearer for set; on ErrUnauthorized it drops the cached
// token, fetches once more and retries once (spec § 4.3).
func Call[T any](ctx context.Context, s Session, set byok.Capability, fn func(byok.Credentials, byok.Token) (T, error)) (T, error) {
	var zero T
	tok, err := s.Tokens.Bearer(ctx, s.P, s.Conn, set)
	if err != nil {
		return zero, err
	}
	v, err := fn(s.Credentials(), tok)
	if !errors.Is(err, byok.ErrUnauthorized) {
		return v, err
	}
	s.Tokens.Invalidate(ctx, s.Conn)
	if tok, err = s.Tokens.Bearer(ctx, s.P, s.Conn, set); err != nil {
		return zero, err
	}
	return fn(s.Credentials(), tok)
}

// Balance and StatementPage are the two banking calls, rate-limited per connection.
func (s Session) Balance(ctx context.Context, at byok.Date) (byok.Balance, error) {
	if err := s.Limits.Banking(ctx, s.Conn); err != nil {
		return byok.Balance{}, err
	}
	return Call(ctx, s, byok.CapabilityStatement, func(c byok.Credentials, t byok.Token) (byok.Balance, error) {
		return s.P.Balance(ctx, c, t, at)
	})
}

func (s Session) StatementPage(ctx context.Context, from, to byok.Date, page int) (byok.StatementPage, error) {
	if err := s.Limits.Banking(ctx, s.Conn); err != nil {
		return byok.StatementPage{}, err
	}
	return Call(ctx, s, byok.CapabilityStatement, func(c byok.Credentials, t byok.Token) (byok.StatementPage, error) {
		return s.P.StatementPage(ctx, c, t, from, to, page)
	})
}

// Payout and GetPayout are the money-out calls, with their own token set (only
// the payout scopes) and their own rate limit.
func (s Session) Payout(ctx context.Context, in byok.PayoutRequest) (byok.Payout, error) {
	if err := s.Limits.Payout(ctx, s.Conn); err != nil {
		return byok.Payout{}, err
	}
	return Call(ctx, s, byok.CapabilityPayout, func(c byok.Credentials, t byok.Token) (byok.Payout, error) {
		return s.P.Payout(ctx, c, t, in)
	})
}

func (s Session) GetPayout(ctx context.Context, providerRef string) (byok.Payout, error) {
	return Call(ctx, s, byok.CapabilityPayout, func(c byok.Credentials, t byok.Token) (byok.Payout, error) {
		return s.P.GetPayout(ctx, c, t, providerRef)
	})
}

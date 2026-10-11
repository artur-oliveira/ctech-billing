package problem

import (
	"fmt"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

func TestByokErrorsHaveTheirOwnCodes(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{byok.ErrCertificateExpired, 422, "byok_certificate_expired"},
		{byok.ErrCertificateInvalid, 422, "byok_certificate_invalid"},
		{byok.ErrKeyMismatch, 422, "byok_key_mismatch"},
		{byok.ErrCredentialsRefused, 422, "byok_credentials_refused"},
		{byok.ErrScopesRefused, 422, "byok_credentials_refused"},
		{byokrpc.ErrSandboxBusy, 429, "byok_sandbox_busy"},
		{byok.ErrUnavailable, 503, "byok_provider_unavailable"},
		{byok.ErrRateLimited, 503, "byok_provider_unavailable"},
		{byok.ErrInvalidRequest, 422, "byok_invalid_request"},
		{services.ErrByokNotConnected, 404, "byok_not_connected"},
		{services.ErrSandboxInLive, 409, "byok_sandbox_test_only"},
		{repositories.ErrConnectionExists, 409, "byok_already_connected"},
		{services.ErrConnectNotAuthorized, 422, "validation_error"},
		{services.ErrBackfillRange, 422, "validation_error"},
	} {
		p := FromError(fmt.Errorf("wrapped: %w", c.err))
		if p.Status != c.status || p.Code != c.code {
			t.Errorf("%v: %d %s, want %d %s", c.err, p.Status, p.Code, c.status, c.code)
		}
	}
	if p := FromError(services.ErrByokSpaceKind); p.Status != 404 || p.Type != TypeSpaceNotFound {
		t.Errorf("a personal space is the same 404 as an unknown space: %+v", p)
	}
}

package realtime

import "strings"

// Scope kinds. A scope is an opaque key; a change is written once per scope
// that should hear it and a connection is registered under exactly the scopes
// its principal owns (docs/specs/2026-10-14-realtime.md § 4.1).
const (
	KindTenant  = "tenant"
	KindSpace   = "space"
	KindUser    = "user"
	KindInvoice = "invoice"

	scopeSep = ":"
)

func modeName(live bool) string {
	if live {
		return "live"
	}
	return "test"
}

// TenantScope is what a merchant's console hears: "tenant:{org}:{live|test}".
func TenantScope(org string, live bool) string {
	return KindTenant + scopeSep + org + scopeSep + modeName(live)
}

// SpaceScope is what Finanças of one space hears. spaceKey is
// space.ResolvedSpace.PK(), which already carries the mode.
func SpaceScope(spaceKey string) string { return KindSpace + scopeSep + spaceKey }

// UserScope is what the portal of one signed-in user hears.
func UserScope(sub string) string { return KindUser + scopeSep + sub }

// InvoiceScope is what the checkout page of one invoice hears:
// "invoice:{org}:{mode}:{id}".
func InvoiceScope(org string, live bool, invoiceID string) string {
	return KindInvoice + scopeSep + org + scopeSep + modeName(live) + scopeSep + invoiceID
}

// Kind is a scope's kind, or "" for anything that is not one.
func Kind(scope string) string {
	kind, _, ok := strings.Cut(scope, scopeSep)
	if !ok {
		return ""
	}
	switch kind {
	case KindTenant, KindSpace, KindUser, KindInvoice:
		return kind
	}
	return ""
}

package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// registerIntegrations mounts the merchant's integration tools (spec § 5) on
// the console group, which already resolved the organization, the person's
// role (owner/admin) and the mode. Reads take the organization read scope;
// every write the organization write scope.
func registerIntegrations(console fiber.Router, ch *consoleHandlers) {
	read := middleware.RequireUserScope(middleware.ScopeOrganizationRead)
	write := middleware.RequireUserScope(middleware.ScopeOrganizationWrite)

	console.Get("/credentials", read, ch.listCredentials)
	console.Post("/credentials", write, ch.connectCredential)
	console.Delete("/credentials/:client_id", write, ch.disconnectCredential)
	console.Get("/webhooks", read, ch.listWebhooks)
	console.Post("/webhooks", write, ch.createWebhook)
	console.Delete("/webhooks/:id", write, ch.deleteWebhook)
	console.Post("/webhooks/:id/rotate-secret", write, ch.rotateWebhookSecret)
	console.Post("/webhooks/:id/test", write, ch.testWebhook)
	// The history: what an endpoint was sent, and the events of an invoice or a
	// subscription with where each went.
	console.Get("/webhooks/:id/deliveries", read, ch.endpointDeliveries)
	console.Get("/events/:id", read, ch.eventDetail)
	console.Get("/invoices/:id/events", middleware.RequireUserScope(middleware.ScopeInvoicesRead), ch.invoiceEvents)
	console.Get("/subscriptions/:id/events", middleware.RequireUserScope(middleware.ScopeSubscriptionsRead), ch.subscriptionEvents)
}

// isSuspended reports a suspended live tenant (part 2, spec § 4.3). Scope
// decision 1: such a tenant may disconnect, delete and rotate, never connect,
// create or test. Part 2 owns the flag; this is the one call site.
func isSuspended(c fiber.Ctx) bool {
	org := middleware.GetOrganization(c)
	return org != nil && org.Suspended()
}

func refuseSuspended(c fiber.Ctx) error {
	return problem.Forbidden("this organization's live mode is suspended").WithCode("tenant_suspended").Send(c)
}

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// operatorArgs is what every tenant-zero operator action needs: live, a named
// operator, and the tenant that is configured.
func operatorArgs(mode, operator, tenant string) error {
	switch {
	case mode != "live":
		return errors.New("needs -mode=live (no default on purpose)")
	case operator == "":
		return errors.New("needs -operator=<name>")
	case tenant == "":
		return errors.New("PORTAL_ORGANIZATION_ID (tenant zero) is not set")
	}
	return nil
}

// tenantRefresh re-tests tenant zero's stored bank credentials and records what
// they can do now (a connection made before the bank's product list grew).
func tenantRefresh(ctx context.Context, cfg *config.Config, mode, operator string) int {
	if err := operatorArgs(mode, operator, cfg.PortalOrganizationID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	op, err := app.BuildByokOperator(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		return 1
	}
	tenant, err := op.Orgs.Get(ctx, cfg.PortalOrganizationID, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reading tenant zero:", err)
		return 1
	}
	sp, err := space.ForJob(tenant.AccountOrganizationID, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	now := time.Now()
	conn, err := op.Conns.Refresh(ctx, sp, provider, "operator:"+operator, "tenant-refresh-"+now.UTC().Format("20060102T150405Z"), now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "not refreshed:", err)
		return 1
	}
	fmt.Printf("connection %s (version %d): capabilities=%v missing_scopes=%v\n", conn.ID, conn.Version, conn.Capabilities, conn.MissingScopes)
	return 0
}

// tenantPayout pays a Pix out of tenant zero's account to a CPF key, as an
// audited operator: the live proof of the payout path, and an operator's way to
// settle by hand what the wallet cannot. It moves real money, so it prints what
// it will do and needs -yes.
func tenantPayout(ctx context.Context, cfg *config.Config, mode, operator, cpf, reference string, cents int64, yes bool) int {
	if err := operatorArgs(mode, operator, cfg.PortalOrganizationID); err != nil || reference == "" || cents <= 0 || cpf == "" {
		if err == nil {
			err = errors.New("needs -payout-amount-cents, -payout-reference and the CPF")
		}
		fmt.Fprintln(os.Stderr, "usage: byok -tenant-payout=<cpf> -mode=live -operator=<name> -payout-amount-cents=<n> -payout-reference=<ref> [-yes]:", err)
		return 2
	}
	if !yes {
		fmt.Printf("dry run: would pay %s to the Pix key of the CPF ending %s (reference %s). Pass -yes to send it.\n",
			billing.Cents(cents), last4(cpf), reference)
		return 0
	}
	op, err := app.BuildByokOperator(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		return 1
	}
	p, created, err := op.Payouts.Create(ctx, cfg.PortalOrganizationID, true,
		services.PayoutInput{Reference: reference, CPF: cpf, AmountCents: cents, Message: "Prova de saque CTech"}, time.Now())
	switch {
	case errors.Is(err, services.ErrPayoutConflict):
		fmt.Fprintln(os.Stderr, "that reference already names another payout")
		return 1
	case errors.Is(err, services.ErrPayoutNotAllowed):
		fmt.Fprintln(os.Stderr, "tenant zero's connection cannot pay out: refresh it (-tenant-refresh) and check the Inter app has the payment permissions")
		return 1
	case err != nil:
		fmt.Fprintln(os.Stderr, "not sent:", err)
		return 1
	}
	verb := "already existed"
	if created {
		verb = "sent"
	}
	fmt.Printf("payout %s %s: status=%s amount=%d reason=%q\n", p.ID, verb, p.Status, int64(p.Amount), p.FailureReason)
	return 0
}

func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

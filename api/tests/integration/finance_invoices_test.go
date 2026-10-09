//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/provision"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Spec § 3.8 and the 6.7 plan: a paid billing invoice is revenue in the issuing
// organization's space and an expense in the paying person's own space.

func provisionRepos() provision.Repos {
	return provision.Repos{
		Organizations: repositories.NewOrganizationRepository(testDB, testCfg),
		Credentials:   repositories.NewCredentialRepository(testDB, testCfg),
		Catalog:       repositories.NewCatalogRepository(testDB, testCfg),
		Webhooks:      repositories.NewWebhookRepository(testDB, testCfg),
	}
}

// An existing tenant (tenant zero is one in every environment) gains its link
// from the plan once; the same plan again changes nothing; a plan that names a
// different organization is refused and the stored link stays.
func TestProvisioningLinksAnExistingTenantOnce(t *testing.T) {
	ctx := ctxT(t)
	repos := provisionRepos()
	plan := &provision.Plan{Organization: provision.Organization{ID: "org_" + id.New(), DisplayName: "CTech"}}
	if _, err := provision.Apply(ctx, repos, plan, true, now()); err != nil {
		t.Fatal(err)
	}

	link := newSpaceOrgID()
	plan.Organization.AccountOrganizationID = link
	res, err := provision.Apply(ctx, repos, plan, true, now())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(res.Created, "account organization link "+plan.Organization.ID) {
		t.Fatalf("created = %v", res.Created)
	}
	res, err = provision.Apply(ctx, repos, plan, true, now())
	if err != nil || !contains(res.Skipped, "account organization link "+plan.Organization.ID) {
		t.Fatalf("second apply: %v, skipped = %v", err, res.Skipped)
	}

	plan.Organization.AccountOrganizationID = newSpaceOrgID()
	if _, err := provision.Apply(ctx, repos, plan, true, now()); !errors.Is(err, repositories.ErrAccountOrganizationLinked) {
		t.Fatalf("a diverging link: err = %v", err)
	}
	org, err := repos.Organizations.Get(ctx, plan.Organization.ID, true)
	if err != nil || org.AccountOrganizationID != link {
		t.Fatalf("stored link = %q, %v; want %q", org.AccountOrganizationID, err, link)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

package repositories

import (
	"slices"
	"testing"
)

func TestEveryTableIsClassifiedForTheTenantPurge(t *testing.T) {
	for _, name := range TableNames() {
		_, other := tenantPurgeNotTenantScoped[name]
		purged := slices.Contains(tenantTables, name)
		if other == purged {
			t.Errorf("table %q must be in exactly one of tenantTables (cleared under the tenant partition) or tenantPurgeNotTenantScoped (with the reason it is not)", name)
		}
	}
	for _, name := range tenantTables {
		if !slices.Contains(TableNames(), name) {
			t.Errorf("tenantTables names %q, which is not a table", name)
		}
	}
}

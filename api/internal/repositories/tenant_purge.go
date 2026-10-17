package repositories

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// tenantTables are the tables whose rows sit directly under a tenant's mode
// partition TenantPK(id, mode). PurgeTenant clears each one. organizations is
// handled last on its own (the tenant row names nothing else, and its removal is
// what makes `billing.merchant_tenant` stop being reported).
// TestEveryTableIsClassifiedForTheTenantPurge makes a new table choose a side.
var tenantTables = []string{
	TableCredentials, TableCustomers, TableProducts, TablePrices, TableSubscriptions,
	TableInvoices, TableUsage, TableWebhooks, TableAudit, TableIdempotency,
}

// tenantPurgeNotTenantScoped is every table PurgeTenant does not clear under the
// tenant partition, with the reason.
var tenantPurgeNotTenantScoped = map[string]string{
	TableOrganizations:      "the tenant row itself, deleted last and by id; the merchant's application rows are tenant zero's and go through ApplicationRepository.DeleteForOrganization",
	TableByok:               "the tenant's BYOK counters (BYOKCOUNT#/BYOKSUBS#) and charge-reference pointers are deleted by id; the workspace's connections and hooks belong to the workspace, not to the billing tenant",
	TableErasureState:       "the account-deletion saga's own state",
	TableTenantClosures:     "closure state and the tenant's tombstone",
	TableLedgerAccounts:     "finance spaces belong to the workspace (Finanças), not to the billing tenant",
	TableLedgerTransactions: "finance space",
	TableBills:              "finance space",
	TableRecurrences:        "finance space",
	TableCards:              "finance space",
	TableImports:            "finance space",
}

// TenantPurger erases a merchant tenant's data (spec T4). It deletes what lies
// under the tenant's partitions in every tenant-keyed table, the pointer rows
// those rows name, the BYOK counters and the level rows derivable from the
// catalogue and the customers. Sub-partitions it cannot enumerate online
// (`{tenant}#{mode}#USAGE#…`, `#GEN#…`, `#LEVEL#…` of unknown references) are the
// operator command cmd/closure-residue's (it may Scan; the service role may not).
type TenantPurger struct {
	bases     map[string]Base
	levels    *LevelRepository
	catalog   *CatalogRepository
	customers *CustomerRepository
}

func NewTenantPurger(db *dynamodb.Client, cfg *config.Config) *TenantPurger {
	bases := map[string]Base{}
	for _, t := range append(append([]string{}, tenantTables...), TableOrganizations, TableByok) {
		bases[t] = NewBase(db, cfg, t)
	}
	return &TenantPurger{bases: bases,
		levels: NewLevelRepository(db, cfg), catalog: NewCatalogRepository(db, cfg), customers: NewCustomerRepository(db, cfg)}
}

// TenantPurgeReport counts rows deleted (or, in a dry run, found) per source.
type TenantPurgeReport map[string]int

// PurgeTenant erases one billing tenant row (one mode). dryRun only counts.
// Idempotent and resumable: every call re-reads from scratch, and the tenant row
// goes last, so a run that dies anywhere is finished by the next.
func (p *TenantPurger) PurgeTenant(ctx context.Context, t *billing.Organization, dryRun bool) (TenantPurgeReport, error) {
	rep := TenantPurgeReport{}
	pk := TenantPK(t.ID, t.Livemode)

	// 1. Pointer rows named by rows that are about to go: read them first.
	if err := p.pointers(ctx, t, pk, dryRun, rep); err != nil {
		return rep, err
	}
	// 2. The levels derivable from the catalogue and the customers.
	if err := p.eraseLevels(ctx, t, dryRun, rep); err != nil {
		return rep, err
	}
	// 3. BYOK counters of the tenant.
	for _, ptn := range []string{"BYOKCOUNT#" + t.ID, "BYOKSUBS#" + t.ID} {
		n, err := p.clear(ctx, TableByok, ptn, dryRun)
		rep["byok_counters"] += n
		if err != nil {
			return rep, err
		}
	}
	// 4. The tenant's own partition in every tenant-keyed table.
	for _, table := range tenantTables {
		n, err := p.clear(ctx, table, pk, dryRun)
		rep[table] += n
		if err != nil {
			return rep, err
		}
	}
	// 5. The tenant row last.
	n, err := p.clear(ctx, TableOrganizations, pk, dryRun)
	rep[TableOrganizations] += n
	return rep, err
}

// pointers deletes what the tenant's credentials, payment attempts and payouts
// point to outside the tenant partition.
func (p *TenantPurger) pointers(ctx context.Context, t *billing.Organization, pk string, dryRun bool, rep TenantPurgeReport) error {
	// credentials: CLIENT#{id} guard rows
	creds, err := queryPrefix(ctx, p.bases[TableCredentials], pk, skCredential)
	if err != nil {
		return err
	}
	for _, it := range creds {
		clientID := strings.TrimPrefix(strAttrOf(it, "sk"), skCredential)
		if clientID == "" {
			continue
		}
		n, err := p.deleteOne(ctx, TableCredentials, LookupCredentialPK(clientID), skCredentialGuard, dryRun)
		rep["credential_guards"] += n
		if err != nil {
			return err
		}
	}
	// payment attempts: CHARGEREF pointers in byok; payouts: PAYREF markers
	items, err := queryPrefix(ctx, p.bases[TableInvoices], pk, "")
	if err != nil {
		return err
	}
	for _, it := range items {
		sk := strAttrOf(it, "sk")
		switch {
		case strings.Contains(sk, "#ATTEMPT#"):
			provider, ref := strAttrOf(it, "provider"), strAttrOf(it, "charge_ref")
			if provider == "" || ref == "" {
				continue
			}
			n, err := p.deleteOne(ctx, TableByok, ChargeRefPK(provider, t.Livemode, ref), "CHARGEREF", dryRun)
			rep["charge_refs"] += n
			if err != nil {
				return err
			}
		case strings.HasPrefix(sk, "PAYOUT#"):
			ref := strAttrOf(it, "reference")
			if ref == "" {
				continue
			}
			n, err := p.deleteOne(ctx, TableInvoices, LookupPayoutReferencePK(t.ID, t.Livemode, ref), "REF", dryRun)
			rep["payout_refs"] += n
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// eraseLevels removes the level history of every (customer reference, meter)
// the catalogue and the customers name. Other references are the residue
// command's.
func (p *TenantPurger) eraseLevels(ctx context.Context, t *billing.Organization, dryRun bool, rep TenantPurgeReport) error {
	prices, err := p.catalog.ListPrices(ctx, t.ID, t.Livemode, 0)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var meters []string
	for _, pr := range prices {
		if pr.Meter != "" && !seen[pr.Meter] {
			seen[pr.Meter] = true
			meters = append(meters, pr.Meter)
		}
	}
	if len(meters) == 0 {
		return nil
	}
	items, err := queryPrefix(ctx, p.bases[TableCustomers], TenantPK(t.ID, t.Livemode), skCustomer)
	if err != nil {
		return err
	}
	for _, it := range items {
		ref := strAttrOf(it, "external_ref")
		if ref == "" {
			continue
		}
		if dryRun {
			rep["level_refs"]++
			continue
		}
		n, err := p.levels.EraseRef(ctx, t.ID, t.Livemode, ref, meters)
		rep["level_rows"] += n
		if err != nil {
			return err
		}
	}
	return nil
}

// clear deletes (or counts) every row under one partition, a page at a time so a
// large tenant never sits in memory.
func (p *TenantPurger) clear(ctx context.Context, table, pk string, dryRun bool) (int, error) {
	b := p.bases[table]
	total := 0
	for {
		res, err := b.Query(ctx, QueryOpts{PK: pk, ScanIndexForward: true, Limit: 100, ConsistentRead: true})
		if err != nil {
			return total, err
		}
		if len(res.Items) == 0 {
			return total, nil
		}
		if dryRun {
			// A dry run cannot delete, so it pages instead of re-reading page one.
			total += len(res.Items)
			for len(res.LastEvaluatedKey) > 0 {
				res, err = b.Query(ctx, QueryOpts{PK: pk, ScanIndexForward: true, Limit: 100, ConsistentRead: true, ExclusiveStartKey: res.LastEvaluatedKey})
				if err != nil {
					return total, err
				}
				total += len(res.Items)
			}
			return total, nil
		}
		n, err := deleteKeys(ctx, b, res.Items)
		total += n
		if err != nil {
			return total, err
		}
	}
}

// deleteOne deletes (or counts) one row if it exists.
func (p *TenantPurger) deleteOne(ctx context.Context, table, pk, sk string, dryRun bool) (int, error) {
	b := p.bases[table]
	item, err := b.GetItem(ctx, pk, sk)
	if err != nil || item == nil {
		return 0, err
	}
	if dryRun {
		return 1, nil
	}
	if _, err := b.DeleteItem(ctx, pk, sk); err != nil {
		return 0, fmt.Errorf("deleting %s %s: %w", table, pk, err)
	}
	return 1, nil
}

func strAttrOf(it map[string]types.AttributeValue, name string) string {
	if v, ok := it[name].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

// ResidueRow is one row left under a closed tenant's prefix.
type ResidueRow struct{ Table, PK, SK string }

// ResidueReport is what SweepResidue found (and deleted when apply).
type ResidueReport struct {
	Scanned int
	Found   []ResidueRow
	Deleted int
}

// SweepResidue finds every row, in every table, whose partition key begins with
// `{tenantID}#`: the usage, generation-key and level sub-partitions PurgeTenant
// cannot enumerate online. It Scans, which the service role is denied
// (terraform/billing/iam.tf), so only cmd/closure-residue calls it, with an
// operator's credentials. Idempotent; apply=false only reports.
func (p *TenantPurger) SweepResidue(ctx context.Context, tenantID string, apply bool) (ResidueReport, error) {
	var rep ResidueReport
	prefix := tenantID + "#"
	for _, table := range append(append([]string{}, tenantTables...), TableOrganizations) {
		b := p.bases[table]
		var start map[string]types.AttributeValue
		for {
			out, err := b.ScanRaw(ctx, &dynamodb.ScanInput{
				FilterExpression:          aws.String("begins_with(pk, :p)"),
				ProjectionExpression:      aws.String("pk, sk"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":p": &types.AttributeValueMemberS{Value: prefix}},
				ExclusiveStartKey:         start,
			})
			if err != nil {
				return rep, fmt.Errorf("scan %s: %w", table, err)
			}
			rep.Scanned += int(out.ScannedCount)
			for _, it := range out.Items {
				rep.Found = append(rep.Found, ResidueRow{Table: table, PK: strAttrOf(it, "pk"), SK: strAttrOf(it, "sk")})
			}
			if apply && len(out.Items) > 0 {
				n, err := deleteKeys(ctx, b, out.Items)
				rep.Deleted += n
				if err != nil {
					return rep, err
				}
			}
			if len(out.LastEvaluatedKey) == 0 {
				break
			}
			start = out.LastEvaluatedKey
		}
	}
	return rep, nil
}

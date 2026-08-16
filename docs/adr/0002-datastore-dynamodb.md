# ADR 0002 — Datastore: DynamoDB, one table per entity, period GSI for analytics

Status: Accepted (2026-08-15) · Records decision **D7** · Closes the open ADR referenced by
`ARCHITECTURE.md` § 1 and `PLAN.md` § "Open decisions"

> **Amended 2026-08-15 — table decomposition.** The first version of this ADR decided
> "DynamoDB, single table" and never argued the *single table* half; it argued DynamoDB against a
> relational database, and it argued the shape of the period index. Both of those still stand. The
> table count was reopened, examined, and **reversed**: billing now uses one table per entity, the
> same convention as `ctech-dfe` and `ctech-wallet`. The reasoning is under "Why not one table",
> including the argument that was made for one table and does not survive contact with the code.

> **Amended by [ADR 0010](0010-infrastructure-as-terraform.md):** only the *shape* of the period
> index changed — from the multi-attribute keys copied from `ctech-dfe` to a single concatenated
> sort key. The first version of that amendment justified it by saying Terraform could not express a
> multi-attribute key. **It can**, on the provider version this repository pins; the correction and
> the actual reason are under "Indexes".

## Context

`ARCHITECTURE.md` deliberately left storage undecided and warned that billing's workload
(pro-rata, usage aggregation, immutable price versions, period queries) is relational-shaped, so
"wallet uses DynamoDB" was explicitly **not** an argument.

The counter-evidence is that the shape billing actually needs already exists and works in this
ecosystem. `ctech-dfe/api/internal/repositories/documents.go:137-180` answers consolidated period
queries — year, year+month, year+month+day — with no SQL, by constraining a period key **by
prefix**. That is the access pattern billing's § 11 metrics need, and DynamoDB serves it directly.

Running a second datastore technology also costs an operational surface the team does not have.

## Decision

**DynamoDB, on-demand billing, one table per entity.** Period reporting is served by a GSI whose
sort key is queried by prefix. No relational database is introduced.

Every partition key **begins with** `{organization_id}#{livemode}` (see
[ADR 0003](0003-tenant-and-livemode-partition-key.md)). Tenant isolation is a property of the key,
not of the table, and that is why splitting the tables did not weaken it.

### Tables

Physical names follow the company standard `{env}_billing_{table}` — `prod_billing_invoices`, the
same shape as `prod_dfe_nfes`.

| Table | Rows it holds | `pk` | `sk` | Indexes |
|---|---|---|---|---|
| `organizations` | Organization | `{org}#{mode}` | `ORG` | `lookup` |
| `credentials` | APICredential | `{org}#{mode}` | `CREDENTIAL#{client_id}` | `lookup` |
| `customers` | Customer · account→customer pointer | `{org}#{mode}` | `CUSTOMER#{id}` · `CUSTOMER_USER#{user_id}` | `period`, `lookup` |
| `products` | Product | `{org}#{mode}` | `PRODUCT#{id}` | — |
| `prices` | Price | `{org}#{mode}` | `PRICE#{id}` | — |
| `subscriptions` | Subscription · SubscriptionItem | `{org}#{mode}` | `SUB#{id}` · `SUB#{id}#ITEM#{item_id}` | `period`, `schedule` |
| `invoices` | Invoice · InvoiceItem · PaymentAttempt · CheckoutSession · numbering counter · generation marker | `{org}#{mode}` | `INVOICE#{id}` · `…#LINE#{n}` · `…#ATTEMPT#{n}` · `…#CHECKOUT#{id}` · `COUNTER#INVOICE#{year}` | `period`, `schedule`, `lookup` |
| `usage` | UsageRecord | `{org}#{mode}#USAGE#{item}#{period}` | `{idempotency_key}` | — |
| `audit` | AuditLog | `{org}#{mode}` | `AUDIT#{ulid}` | `period` |
| `idempotency` | replayable responses | `{org}#{mode}` | `IDEMPOTENCY#{key}` | — |

**A child lives in its parent's table, under its parent's partition.** That is the one place rows of
different types share a table, and it is not a leftover of the single-table design — it is the
reason the decomposition stops where it does:

- An invoice, its lines, its attempts and its sessions are read together by the invoice screen and
  written together in one transaction. Nested sort keys make each of those a prefix `Query` inside a
  partition the caller was already entitled to read. Splitting them into `invoice_items` and
  `payment_attempts` tables would turn one read into three and give nothing back.
- The invoice numbering counter is in `invoices` for a stronger reason: it is advanced in the same
  transaction that finalizes the invoice, and it is only ever touched there.
- Products and prices are **not** this case. A price is a version of a product, not a child of one:
  nothing reads a product together with its prices, and the console lists them separately. They are
  two tables.

`usage` is the entity with unbounded per-tenant write volume, so its partition key carries the
subscription item and the period. The key still starts with the tenant prefix — no cross-tenant read
is expressible — and period close reads exactly one partition instead of the tenant's whole history.

### The schema is one file

`api/internal/repositories/schema.json` is the only definition of these tables. The Go service
embeds it (`schema.go`); `terraform/billing/dynamodb.tf` reads it with `jsondecode(file(...))` and
builds every table from it with `for_each`.

The previous design had the schema twice — a Go `CreateTableInput` for the integration tests and an
HCL resource for the real table — with a test that parsed the `.tf` with regexes to prove the two
agreed. That test worked. It was also answering a question that should not exist: two definitions of
one schema drift, and the way that drift is found is a query that passes every test and fails in
production. One file with two readers cannot drift, so the test is gone and the class of bug with
it. What remains in `schema_test.go` are checks a single file can still get wrong on its own — a key
naming an undeclared attribute, an index name no query uses.

### Indexes

| Index | Partition key | Sort key | Serves | On |
|---|---|---|---|---|
| `period-index` | `period_pk` = `{org}#{mode}#{ENTITY}` | `period_sk` = `{year}#{month}#{day}#{seq}` | Tenant listings and the § 11 metrics | `customers`, `subscriptions`, `invoices`, `audit` |
| `schedule-index` (sparse) | `schedule_pk` = `{mode}#{job}#{date}` | `schedule_sk` | The cross-tenant sweeps | `subscriptions`, `invoices` |
| `lookup-index` (sparse) | `lookup_pk` | `sk` | Reference lookups by an external identifier | `organizations`, `credentials`, `customers`, `invoices` |

The `ENTITY` segment of `period_pk` survives the split because `invoices` holds three indexed row
types: "invoices issued in March" must not also count the attempts to pay them.

The period index uses a **single derived partition attribute** and a **single concatenated sort
key**, rather than the multi-attribute keys `ctech-dfe` uses (`sortKeys: [year, month, day,
number]`). Both express the same thing — a multi-attribute key can only ever be constrained left to
right, so "everything in 2026" is `begins_with "2026#"` and "everything in March 2026" is
`begins_with "2026#03#"`.

This is a **choice, not a constraint**. An earlier version of this ADR claimed the multi-attribute
form was not expressible in Terraform; that was measured against `hashicorp/aws` 5.x and is wrong
for the 6.x this repository pins — provider 6 replaced the GSI's `hash_key`/`range_key` arguments
with repeatable `key_schema` blocks, an unbounded list that exists for exactly this. Either shape
can be built. The concatenated form is kept because it is one attribute rather than four and needs
only the simplest shared query helper (a prefix `Query`) instead of the composite-key builder.

The segments are **zero-padded**: padding is what makes them sort ("03" before "12"; "3" would sort
after). One constructor writes the key and one helper renders a query prefix, so a read can never
pad differently from a write.

The date in `period_sk` is the civil date in **`America/Sao_Paulo`**, never UTC. An invoice due
01/03 at 00:30 BRT is 28/02 in UTC; a monthly report computed from UTC misreports every month
boundary.

`schedule-index` and `lookup-index` are **sparse**: their key attributes exist only while the item is
actionable (an `ACTIVE` subscription carries `schedule_pk`, a `CANCELED` one does not), so the index
size tracks work-to-do rather than history.

`schedule_pk` is the only key in the system that does **not** start with a tenant — its partition is
`{livemode}#{job}#{date}`. A daily sweep is a system job, not a tenant read path. No request-scoped
code may query it, which is why `cmd/sweep` and `cmd/reconcile` are binaries and not routes: there
is no endpoint to mis-scope, and a scheduler that can run the job does not thereby hold a token that
reads anybody's data. `schema_test.go` pins which tables carry that index, so a third one acquiring
it is a decision somebody makes rather than a line somebody adds.

Two jobs exist today: `SUB_DUE` (written and read) and `CHARGE_RECONCILE` (written and read).
`SETTLEMENT` is **armed with no reader** — it is dunning's input and dunning is not built. The rows
are written now rather than backfilled later, because arming an invoice retroactively means finding
the invoices that should have been armed, which is the query the index exists to avoid.

## Why not one table

The single-table version of this ADR asserted the layout without defending it. When it was
defended, three arguments were offered and only one survived.

**"The sweeps need one index."** They do not. Only three row types carry schedule keys —
subscription, invoice, payment attempt — so per-entity tables mean a `schedule-index` on two tables
and none on `customers`, `products` or `audit`. And the sweep already issues one `Query` per job
partition: `live#SUB_DUE#2026-08-15` and `live#CHARGE_RECONCILE#2026-08-15` are different partitions
whether or not they are in the same index. Sharing the index saved zero round trips. This was the
strongest-sounding argument and it was simply wrong.

**"Transactions need one table."** They do not. A `TransactWriteItem` carries its own table name, so
`TransactWriteItems` spans tables in the same account and region. `CommitStatusChange` now writes an
entity's status change to its table and the audit row to `audit`, in one transaction, unchanged in
every property that mattered.

**"An aggregate should be one partition."** This one holds, and it is the reason the decomposition
stops at the aggregate boundary rather than at the entity boundary. See the table above.

Against that, per-entity tables buy things a single table cannot:

- **Restore granularity.** Point-in-time recovery is per table. A bad migration on subscriptions can
  be restored without dragging invoices through the same recovery.
- **They are readable.** An operator can look at `prod_billing_invoices` and see invoices.
- **Consistency across the company.** `ctech-dfe` and `ctech-wallet` are both per-entity. `CLAUDE.md`
  asks that these repositories be treated as one codebase; billing being the only single-table
  service was a divergence with no argument behind it.

The cost is more Terraform resources and more `Base` instances, and `for_each` over one JSON file
absorbs the first while the repository constructors absorb the second. No call site outside
`internal/repositories` changed.

## Consequences

- Pro-rata and usage aggregation run in Go over one partition, not in SQL. That was already the
  design (`PLAN.md` Phase 1); now it is an obligation.
- The segment order `year#month#day` is fixed forever. A prefix can only be read left to right, so
  reordering it would make "everything in 2026" unqueryable without also naming a month.
- No `Scan` on any tenant read path, and the IAM role denies it explicitly across every table. If an
  access pattern only resolves with `Scan`, an index is missing — that is not permission to scan.
- Adding a table or an index is an edit to `schema.json` and nothing else. Adding one that no Go
  constant names fails `schema_test.go`.

## Limits accepted

`period-index` serves the **pre-declared** metrics of § 11 only. It does not serve ad-hoc queries,
cohorts by arbitrary attribute, or joins across entities — and a join across entities is now also a
join across tables, which makes the limit more visible without making it worse, since DynamoDB never
had joins. When that is asked for — and it will be — the answer is exporting to S3 and querying with
Athena. It is **not** swapping the datastore or growing a reporting schema inside billing. That exit
is a post-MVP roadmap item, recorded here so it is not discovered as hidden debt.

## Reopen if

Someone needs ad-hoc analytical queries over billing data. The answer is the S3/Athena export
above, not a relational migration.

/**
 * The console wire contract, mirroring api/internal/api/v1/console_dto.go and
 * the shared invoice/customer/price payloads in dto.go.
 *
 * The opposite of the portal's types by design: the internal status **is** here,
 * so is the metadata and so is the audit trail. An operator asks "what is the
 * state of this record"; the consumer asks "what am I paying". Two questions,
 * two payloads, and the console must never render the portal's phrasing back at
 * somebody who needs the enum (PRODUCT.md).
 */
import type {Cents, DocumentLink, IsoDate} from "@/lib/api/types"

export type {DocumentLink}

export type InvoiceStatus = "DRAFT" | "OPEN" | "PAID" | "VOID" | "UNCOLLECTIBLE"

export interface ConsoleTenant {
  organization_id: string
  display_name: string
  livemode: boolean
  payout_status: string
  /** The server's answer to whether this organization may collect at all
   *  (ADR 0005). Published so the console can explain the gate, never so it can
   *  decide it. */
  can_charge: boolean
  /** The collection rail (BYOK spec § 7.1): "wallet" or "byok". */
  payout_rail?: "wallet" | "byok"
  payout_provider?: string
  /** When can_charge is false, the problem code saying why. */
  charge_block?: string
}

/** A BYOK charge's late fees, in basis points (200 = 2%). */
export interface CollectionSettings {
  fine_bp: number
  monthly_interest_bp: number
}

/** Money above what an invoice owed, or part of a paid one given back with a
 *  credit note (BYOK spec § 7.4, § 7.5). Never refunded automatically. */
export interface ExcessPayment {
  id: string
  invoice_id: string
  kind: "excess" | "credit_refund"
  amount: Cents
  status: "open" | "refunding" | "refunded" | "refund_failed"
  paid_at: string
  provider: string
}

/** The invoice's Pix on the BYOK rail (spec § 11.3). */
export interface InvoiceByok {
  provider?: string
  attempt_status?: "PENDING" | "SUCCEEDED" | "FAILED" | "ABANDONED"
  payable?: string
  due_date?: string
  payable_until?: string
  late_charges?: Cents
  /** Why there is no payable Pix, as a problem code. */
  reason?: string
  can_retry: boolean
  simulate: boolean
  excess_payments: ExcessPayment[]
}

/** Where an organization stands as a merchant (spec § 3). Display data; every
 *  billing-half request is re-authorized by the server. */
export type MerchantStatus = "none" | "test" | "live" | "rejected" | "suspended"

/** One organization the person may run billing for (owner or admin). */
export interface MerchantOrganization {
  organization_id: string
  /** The X-Billing-Space value; never built from anything else. */
  selector: string
  display_name: string
  role: "owner" | "admin"
  merchant_status: MerchantStatus
  /** Tenant zero's organization: the console shows Solicitações. Display data; the server decides. */
  can_review_applications?: boolean
  /** The latest application's status; absent when the organization never applied (a test
   *  tenant CTech provisioned by hand is `merchant_status: "test"` with no application). */
  application_status?: ApplicationStatus
}

/** Where a merchant application stands (spec § 4). */
export type ApplicationStatus = "pending" | "approved" | "rejected"

/** The applicant's own estimate; labels live in console.volume.*. */
export type MonthlyVolume = "up_to_10k" | "10k_to_50k" | "50k_to_200k" | "over_200k"

/** One of the organization's companies, from ctech-account. */
export interface MerchantCompany {
  company_id: string
  legal_name: string
  trade_name?: string
  tax_id: string
}

/** The form's picker: ctech-account lists at most 200 by legal name and says when it cut the list. */
export interface MerchantCompanyList {
  companies: MerchantCompany[]
  truncated: boolean
}

export interface MerchantApplication {
  id: string
  status: ApplicationStatus
  organization_id: string
  organization_name: string
  company: {id: string; legal_name: string; tax_id: string}
  description: string
  website?: string
  expected_monthly_volume?: MonthlyVolume
  contact_email: string
  applied_by: string
  applied_at: string
  decided_at?: string
  rejection_reason?: string
  /** An approved merchant's live collection rail (BYOK spec § 11.3). */
  payout?: {rail: "wallet" | "byok"; provider?: string; status: string}
}

export interface ApplyRequest {
  company_id: string
  description: string
  website?: string
  expected_monthly_volume?: MonthlyVolume
  contact_email: string
}

/** GET /v1.0/console/session (ADR 0028). */
export interface ConsoleSession {
  organizations: MerchantOrganization[]
  /** ctech-account did not answer: the list is empty because it is unknown. */
  organizations_unavailable: boolean
}

export interface AuditEntry {
  id: string
  action: string
  cause?: string
  actor: string
  before?: string
  after?: string
  request_id?: string
  created_at: string
}

/** A service window as the API sends it on the console: the domain's own
 *  field names. The portal maps it to `start`/`end` server-side (portal DTO);
 *  the console does not, and reading `start` here is reading `undefined`. */
export interface BillingPeriod {
  period_start: IsoDate
  period_end: IsoDate
}

export interface ConsoleInvoiceLine {
  description: string
  period?: BillingPeriod
  quantity: number
  unit_amount: Cents
  amount: Cents
  proration: boolean
}

export interface ConsoleInvoice {
  id: string
  number?: number
  customer_id: string
  subscription_id?: string
  status: InvoiceStatus
  /** Derived on read, never stored — which is why "overdue" is not a status. */
  overdue: boolean
  period: BillingPeriod
  due_date: IsoDate
  currency: string
  subtotal: Cents
  discount: Cents
  total: Cents
  amount_paid: Cents
  amount_due: Cents
  attempt_count: number
  lines?: ConsoleInvoiceLine[]
  metadata?: Record<string, string>
  /** The signed public payment link. Absent unless the invoice is payable — a
   *  console that builds this URL itself is one invoice state away from sending
   *  a customer to a 404. */
  checkout_url?: string
  livemode: boolean
}

/** One row of C2: the invoice, plus the customer's name — an id answers half
 *  of "who owes what". */
export interface ConsoleInvoiceRow extends ConsoleInvoice {
  customer_name?: string
}

export interface CreditNote {
  id: string
  invoice_id: string
  amount: Cents
  currency: string
  reason: string
  refunded_externally: boolean
  external_refund_ref?: string
  created_by: string
  created_at: string
}

export interface ConsoleInvoiceDetail {
  invoice: ConsoleInvoice
  /** Absent when the customer row could not be read: a worse row, not a broken
   *  page. */
  customer_name?: string
  credit_notes?: CreditNote[]
  credited: Cents
  /** What the screen renders as "estornada". Not a status: an invoice that was
   *  paid and then fully credited is still a paid invoice. */
  fully_credited: boolean
  timeline: AuditEntry[]
  /** Absent on the wallet rail. */
  byok?: InvoiceByok
}

export interface ConsoleCustomer {
  id: string
  external_ref?: string
  name: string
  email?: string
  tax_id_masked?: string
  anonymized: boolean
  metadata?: Record<string, string>
  livemode: boolean
}

export type SubscriptionStatus =
  | "INCOMPLETE"
  | "TRIALING"
  | "ACTIVE"
  | "PAST_DUE"
  | "PAUSED"
  | "CANCELED"

export interface Recurrence {
  interval: string
  count: number
}

export interface ConsoleSubscription {
  id: string
  customer_id: string
  status: SubscriptionStatus
  /** The server's answer to "may this customer use the product right now". It
   *  is not `status === "ACTIVE"`: a trial is entitled and a past-due
   *  subscription may still be, which is a policy decision and not a screen's. */
  entitled: boolean
  recurrence: Recurrence
  billing_timing: string
  anchor: IsoDate
  current_period: BillingPeriod
  cancel_at_period_end: boolean
  metadata?: Record<string, string>
  livemode: boolean
}

/** One row of C4 (and of C7's subscriptions): the subscription, plus the names
 *  an operator reads it by. `plan_name` is the products its items bill, joined
 *  with " + ". Either is absent when the server could not read it, and the
 *  screen falls back to the id. */
export interface ConsoleSubscriptionRow extends ConsoleSubscription {
  customer_name?: string
  plan_name?: string
}

export interface ConsolePrice {
  id: string
  product_id: string
  type: "fixed" | "metered"
  currency: string
  unit_amount: Cents
  recurrence: Recurrence
  billing_timing: string
  archived: boolean
  metadata?: Record<string, string>
}

export type DunningAction = "remind" | "escalate" | "abandon"

export interface DunningStep {
  /** Days from the due date. Negative is before it. */
  offset: number
  action: DunningAction
}

export interface DunningPolicy {
  steps: DunningStep[]
  /** Whether this was written here or inherited. The steps are the same either
   *  way — an inherited policy is in force, not absent — and only the label
   *  differs. */
  custom: boolean
}

export interface Issuer {
  legal_name?: string
  tax_id?: string
  address?: string
  email?: string
}

export interface ConsoleSettings {
  organization: ConsoleTenant
  dunning: DunningPolicy
  /** What the invoice PDF is headed by. Empty is a real state the screen has to
   *  point out: documents are going out with no legal name on them. */
  issuer: Issuer
  /** Whether this deployment renders PDFs at all. Configuring the issuer of
   *  documents that are never produced would be configuring nothing. */
  documents_enabled: boolean
  collection: CollectionSettings
  /** Facts, not fields: numbering has no options and retention is a constant.
   *  Published so the screen can state them rather than leave an operator
   *  guessing what it controls. */
  /** Enum code (`sequential_per_year_gapless`); the words are the catalog's. */
  numbering: string
  /** Enum code (`invoices_credit_notes_permanent_audit_5y`). */
  retention: string
}

export interface ConsoleOverview {
  year: number
  month: number
  received: Cents
  open: Cents
  overdue: Cents
  drafts: number
  uncollectible: number
  overdue_count: number
  counted: number
  /** False when the month has more invoices than one page. The screen must say
   *  so rather than present a partial sum as a total. */
  complete: boolean
}

export interface ConsoleProduct {
  id: string
  name: string
  active: boolean
  owner_key?: string
  dunning?: DunningPolicy
  metadata?: Record<string, string>
  /** Archived prices are returned too: a subscription created under one keeps
   *  billing at it, and hiding it makes that invoice look like it came from
   *  nowhere. */
  prices?: ConsolePrice[]
  livemode: boolean
}

export interface ConsoleSubscriptionItem {
  id: string
  price_id: string
  quantity: number
  price: ConsolePrice
}

export interface ConsoleSubscriptionDetail {
  subscription: ConsoleSubscription
  items: ConsoleSubscriptionItem[]
  timeline: AuditEntry[]
}

export interface ConsoleCustomerDetail {
  customer: ConsoleCustomer
  subscriptions: ConsoleSubscriptionRow[]
  timeline: AuditEntry[]
}

export interface ConsolePage<T> {
  data: T[]
  has_more: boolean
  cursor?: string
}

/** A merchant's API credential: their own ctech-account OAuth client, connected by id (spec § 5.1).
 *  `provisioned` is one the tenant plan created (CTech's own services); it cannot be removed here. */
export interface ConsoleCredential {client_id: string; livemode: boolean; active: boolean; provisioned: boolean; connected_by?: string; connected_by_me: boolean; connected_at?: string; description?: string}
/** The credential list, with the scopes a connectable client may hold (the developer link preselects them). */
export interface ConsoleCredentialList {data: ConsoleCredential[]; machine_scopes: string[]}
/** An endpoint is disabled by the delivery job after repeated failures. */
export type WebhookStatus = "active" | "disabled"
/** A webhook endpoint as the console lists it; never carries the secret. `events: []` is every event. */
export interface ConsoleWebhookEndpoint {id: string; url: string; events: string[]; status: WebhookStatus; failure_run: number; disabled_at?: string; provisioned: boolean; added_at?: string; owner_key?: string}
/** The endpoint list, with the subscribable event types and the per-mode limit. */
export interface ConsoleWebhookList {data: ConsoleWebhookEndpoint[]; event_types: string[]; max_endpoints: number}
/** Only create and rotate answer with this; the secret is never shown again. */
export interface ConsoleWebhookWithSecret extends ConsoleWebhookEndpoint {secret: string}

/** One public BYOK plan (spec § 12.5); amounts in centavos, -1 is unlimited. */
export interface BillingPlanOption {
  plan: string
  fee: number
  included: number
  overage_unit: number
  refuse: boolean
  bank_connections: number
  users: number
  api_access: boolean
}

/** Console → Plano: the organization's plan, this month's use and the catalogue. */
export interface BillingPlanState {
  current: {plan: string; subscription_id: string}
  usage: {
    charges_this_month: number
    quota: number
    subscriptions_active: number
    subscriptions_quota: number
    bank_connections: number
    bank_connections_quota: number
  }
  plans: BillingPlanOption[]
}

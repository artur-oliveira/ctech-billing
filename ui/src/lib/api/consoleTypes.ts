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
import type {Cents, IsoDate, Period} from "@/lib/api/types"

export type InvoiceStatus = "DRAFT" | "OPEN" | "PAID" | "VOID" | "UNCOLLECTIBLE"

export interface ConsoleSession {
  organization_id: string
  display_name: string
  livemode: boolean
  payout_status: string
  /** The server's answer to whether this organization may collect at all
   *  (ADR 0005). Published so the console can explain the gate, never so it can
   *  decide it. */
  can_charge: boolean
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

export interface ConsoleInvoiceLine {
  description: string
  period?: Period
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
  period: Period
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
  current_period: Period
  cancel_at_period_end: boolean
  metadata?: Record<string, string>
  livemode: boolean
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

export interface ConsoleProduct {
  id: string
  name: string
  active: boolean
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
  subscriptions: ConsoleSubscription[]
  timeline: AuditEntry[]
}

export interface ConsolePage<T> {
  data: T[]
  has_more: boolean
  cursor?: string
}

/**
 * The portal wire contract, mirroring api/internal/api/v1/portal_dto.go.
 *
 * Note what is not here: no status enum, no metadata, no organization, no
 * audit. That is not the UI declining to read those fields — ADR 0012 keeps
 * them out of the payload entirely. If a screen ever seems to need one, the
 * question is which server-supplied phrase is missing, not which internal
 * field to expose.
 */

/** A civil date in São Paulo, `YYYY-MM-DD`. Never a Date object on the wire. */
export type IsoDate = string

/** Centavos. Money is an integer everywhere in this system. */
export type Cents = number

/**
 * The closed set the API emits alongside every human-readable state, ordered
 * by how much it wants the reader's attention. The UI switches on this and
 * never on the phrase, so wording can change without breaking a screen.
 */
export type Tone = "neutral" | "positive" | "attention" | "urgent"

export interface Period {
  start: IsoDate
  end: IsoDate
}

export interface InvoiceLine {
  description: string
  amount: Cents
  /** A partial period. Named on screen, because an unexplained part-charge is
   *  the most common "what is this?" support message. */
  proration: boolean
}

export interface Invoice {
  id: string
  number?: number
  description: string
  /** Already a sentence: "Vence em 3 dias". Never format this. */
  state: string
  tone: Tone
  due_date: IsoDate
  total: Cents
  amount_paid?: Cents
  amount_due: Cents
  currency: string
  period: Period
  /** The civil date it was settled, absent until it is. A date, not an
   *  instant: a receipt is read as "paguei no dia 17". */
  paid_on?: IsoDate
  /** The server's answer to "is this bill done with". Never derive it from the
   *  amounts: a Free-plan invoice is issued and settled with nothing paid, so
   *  `amount_paid > 0` calls it unpaid. */
  settled: boolean
  lines?: InvoiceLine[]
  /** The server's answer to "is there anything for this person to do". A UI
   *  that derives this from the state string will offer to pay a voided
   *  invoice. */
  payable: boolean
}

export interface Subscription {
  id: string
  description: string
  state: string
  tone: Tone
  /** Absent once it is ending or ended: a date that will not happen is worse
   *  than no date. */
  renews_on?: IsoDate
  amount?: Cents
  /** Amount is only known when the period closes. Say that rather than show a
   *  confident zero. */
  metered: boolean
  currency?: string
  current_period: Period
  /** When this subscription started — "cliente desde". */
  since?: IsoDate
  /** The last few invoices this plan produced, newest first. Populated on the
   *  detail only; the list endpoint leaves it out rather than fetching every
   *  subscription's history to render rows that never show it. */
  recent_invoices?: Invoice[]
  cancelable: boolean
}

export interface PixPayment {
  method: string
  pix_code: string
  expires_at: string
}

export interface PaymentResult {
  invoice: Invoice
  payment: PixPayment
}

export interface Session {
  customer_id: string
  name: string
  email?: string
  /** When this person became a customer. */
  since?: IsoDate
  /** Whether this person agreed to the billing terms addendum **in force**.
   *  The server compares versions and publishes only the answer, so a stale
   *  acceptance reads as `false` here and re-gates on the next visit. */
  terms_accepted: boolean
}

export interface ListResponse<T> {
  data: T[]
  has_more: boolean
  cursor?: string
}

/**
 * The payment-link payload, mirroring `checkoutResponse` in
 * api/internal/api/v1/checkout.go.
 *
 * Deliberately smaller than `Invoice`: no id, no period, no `total`, no
 * `amount_paid`, and above all no name, e-mail or tax id. A payment link gets
 * forwarded, and the guarantee that forwarding it discloses nothing is that the
 * data is not in the response at all (ADR 0009 § minimization). Anything a
 * checkout screen seems to need and cannot find here is a question for the
 * server, not a field to add on this side.
 */
export interface CheckoutInvoice {
  number?: number
  description: string
  state: string
  tone: Tone
  due_date: IsoDate
  amount_due: Cents
  currency: string
  lines?: InvoiceLine[]
  payable: boolean
}

export interface Checkout {
  /** Who is being paid. Without it the page asks somebody to send money to
   *  nobody in particular, which is what a phishing page looks like. */
  merchant: string
  invoice: CheckoutInvoice
  /** Present only after the page asks to pay: merely opening a link must never
   *  open a charge. */
  payment?: PixPayment
}

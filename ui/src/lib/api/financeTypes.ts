/**
 * The finance wire contract, mirroring api/internal/api/v1/finance_*.go.
 * Money is integer centavos; dates are civil `YYYY-MM-DD` strings in São Paulo.
 */
import type {Cents, IsoDate} from "@/lib/api/types"

export type Direction = "payable" | "receivable"
export type Bucket = "overdue" | "today" | "upcoming"
export type BillStatus = "forecast" | "paid" | "canceled"
export type AccountClass = "asset" | "liability" | "income" | "expense" | "equity"
export type DREGroup = "gross_revenue" | "deductions" | "costs" | "operating_expenses" | "financial_result" | "other"
export type Verb = "finance.read" | "finance.write" | "finance.settle" | "finance.import" | "finance.configure"
export type Adjust = "none" | "roll_forward"

export interface Bill {
  id: string
  direction: Direction
  amount: Cents
  account_id: string
  category_id: string
  description?: string
  competence_date: IsoDate
  due_date: IsoDate
  paid_date?: IsoDate
  status: BillStatus
  origin: string
  origin_ref?: string
  auto_settle: boolean
  bucket?: Bucket
}

/** The 6.1 stored form of a temporal expression (a tagged union). */
export type ExpressionJSON =
  | {kind: "day_of_month"; day: number}
  | {kind: "workday_of_month"; n: number}
  | {kind: "nth_weekday_of_month"; weekday: number; n: number}
  | {kind: "weekly"; weekday: number; every: number; anchor: IsoDate}
  | {kind: "yearly"; month: number; day: number}
  | {kind: "months_of_year"; months: number[]}
  | {kind: "dates"; dates: IsoDate[]}
  | {kind: "difference"; include: ExpressionJSON; exclude: ExpressionJSON}

export interface Recurrence {
  id: string
  direction: Direction
  amount: Cents
  category_id: string
  account_id: string
  description?: string
  expression: ExpressionJSON
  start: IsoDate
  end?: IsoDate
  business_day_adjust: Adjust
  auto_settle: boolean
  archived: boolean
}

export interface Occurrence {
  nominal: IsoDate
  due: IsoDate
}

/** paid; forecast (made, not yet due); overdue (made, past due, unpaid); skipped (its bill was cancelled). */
export type OccurrenceState = "paid" | "forecast" | "overdue" | "skipped"

/** A bill a recurrence made, for F4's detail (UX batch 3). */
export interface OccurrenceBill extends Occurrence {
  bill_id: string
  amount: Cents
  state: OccurrenceState
  paid_date?: IsoDate
  /** The bill's own flag: an open one that has it is still paid by the daily job after the recurrence ends. */
  auto_settle?: boolean
}

/** `history`: the latest bills it made, oldest first. `upcoming`: dates it will make, none once archived. */
export interface RecurrenceOccurrences {
  history: OccurrenceBill[]
  upcoming: Occurrence[]
}

export interface ProjectionMonth {
  month: string // YYYY-MM
  receivable: Cents
  payable: Cents
  /** Net of recurrence occurrences not yet materialised — never mixed into the two above. */
  virtual: Cents
  /** The two sides of `virtual`, both >= 0. Absent on an older API: derive them from the sign of `virtual`. */
  virtual_receivable?: Cents
  virtual_payable?: Cents
}

export interface Account {
  id: string
  name: string
  class: AccountClass
  dre_group?: DREGroup
  system: boolean
  /** Stable key of a default account or category; the UI translates its name while it is unchanged. */
  system_key?: string
  archived: boolean
  balance: Cents
}

export interface Settings {
  default_receiving_account_id?: string
  /** "Lançar minhas faturas da CTech automaticamente". Absent means on. */
  post_ctech_invoices?: boolean
}

/** personal_default is "Pessoal" (USER#{sub}); the other two are ctech-account workspaces (ADR 0027). */
export type SpaceKind = "personal_default" | "personal" | "organization"

export interface FinanceSpaceEntry {
  /** The X-Billing-Space value: "personal" or "org:{id}". */
  selector: string
  kind: SpaceKind
  display_name: string
  role?: string
  verbs: Verb[]
  /** The owner of a personal workspace: may open its people page in ctech-account. */
  manage_people: boolean
}

export interface FinanceSpaces {
  spaces: FinanceSpaceEntry[]
  organizations_unavailable: boolean
}

export interface CurrentSpace {
  kind: SpaceKind
  organization_id?: string
  mode: "live" | "test"
  verbs: Verb[]
}

export interface ListResponse<T> {
  data: T[]
  has_more: boolean
  cursor?: string
}

export interface NewBill {
  direction: Direction
  amount: Cents
  account_id: string
  category_id: string
  description?: string
  competence_date?: IsoDate
  due_date: IsoDate
  auto_settle?: boolean
}

export interface BillPatch {
  amount?: Cents
  category_id?: string
  account_id?: string
  description?: string
  due_date?: IsoDate
  auto_settle?: boolean
}

export interface Settlement {
  paid_amount?: Cents
  paid_date?: IsoDate
  difference_category_id?: string
}

export interface ScheduleInput {
  expression: ExpressionJSON
  start: IsoDate
  end?: IsoDate
  business_day_adjust?: Adjust
}

export interface NewRecurrence extends ScheduleInput {
  direction: Direction
  amount: Cents
  category_id: string
  account_id: string
  description?: string
  auto_settle?: boolean
}

export interface RecurrencePatch {
  amount?: Cents
  category_id?: string
  account_id?: string
  description?: string
  auto_settle?: boolean
  /** "" removes the end (sent as null): the recurrence has no end again. So does "" on description. */
  end?: IsoDate | ""
  /** Confirms an end that leaves nothing to come: saved and archived in one write.
   *  Without it the API answers 422 `recurrence_would_end` and saves nothing. */
  archive?: boolean
}

export interface PreviewInput extends ScheduleInput {
  from?: IsoDate
  count: number
}

export interface NewAccount {
  name: string
  class: AccountClass
  dre_group?: DREGroup
}

// --- 6.4: statement and reports -----------------------------------------------------

/** What produced a ledger fact; "" on entries posted before 6.4. */
export type TxKind = "" | "recognition" | "settlement" | "transfer" | "opening_balance" | "reversal" | "adjustment" | "card_purchase" | "statement_payment"

export interface StatementEntry {
  transaction_id: string
  date: IsoDate
  /** Signed: positive is money in. */
  amount: Cents
  balance: Cents
  kind: TxKind
  memo: string
  category_id?: string
  bill_id?: string
  reversal: boolean
  reversed: boolean
}

export interface Statement {
  account_id: string
  from: IsoDate
  /** Exclusive. */
  to: IsoDate
  opening: Cents
  closing: Cents
  entries: StatementEntry[]
}

export interface CashFlowMonth {
  month: string
  in: Cents
  out: Cents
  openings: Cents
  /** category_id "" is "Sem categoria". */
  lines: {category_id: string; amount: Cents}[]
}

export interface CashFlow {
  from: string
  to: string
  opening_cash: Cents
  closing_cash: Cents
  months: CashFlowMonth[]
}

export interface DREGroupLine {
  group: DREGroup
  categories: {category_id: string; amounts: Cents[]; total: Cents}[]
  amounts: Cents[]
  total: Cents
}

export interface DRE {
  months: string[]
  groups: DREGroupLine[]
  result: Cents[]
  total: Cents
}

export interface NewTransfer {
  from_account_id: string
  to_account_id: string
  amount: Cents
  date: IsoDate
  memo?: string
}

export interface OpeningBalance {
  amount: Cents
  date: IsoDate
}

// --- 6.5: cards ------------------------------------------------------------------------

/** The network printed on a card (UX batch 3): the API's closed set. */
export type CardBrand = "visa" | "mastercard" | "elo" | "amex" | "hipercard" | "diners" | "other"

export interface Card {
  id: string
  name: string
  closing_day: number
  due_day: number
  paying_account_id: string
  /** `YYYY-MM`: the first statement not yet closed. */
  open_month: string
  /** The card account's balance: negative is what is owed. */
  balance: Cents
  archived: boolean
  brand?: CardBrand
  /** Exactly four digits, only to tell cards apart. */
  last4?: string
}

export interface NewCard {
  name: string
  closing_day: number
  due_day: number
  paying_account_id: string
  brand?: CardBrand
  last4?: string
}

export interface CardPatch {
  closing_day?: number
  due_day?: number
  paying_account_id?: string
  /** "" clears it (sent as null, lib/api/finance patchBody). */
  brand?: CardBrand | ""
  /** "" clears it (sent as null, lib/api/finance patchBody). */
  last4?: string
}

export type StatementItemKind = "installment" | "credit" | "advance" | "carry"

export interface StatementItem {
  purchase_id: string
  description: string
  category_id?: string
  date: IsoDate
  /** 3 of 12; absent on a credit, an advance or a carry. */
  number?: number
  of?: number
  kind: StatementItemKind
  /** Positive charges, negative credits. */
  amount: Cents
}

export type CardStatementStatus = "open" | "future" | "closed" | "paid"

export interface CardStatement {
  card_id: string
  month: string
  status: CardStatementStatus
  closing_date: IsoDate
  due_date: IsoDate
  total: Cents
  bill_id?: string
  items: StatementItem[]
}

export interface Purchase {
  id: string
  description: string
  category_id: string
  date: IsoDate
  total: Cents
  installments: {number: number; amount: Cents; month: string}[]
  refunded: boolean
}

export interface NewPurchase {
  date: IsoDate
  description: string
  category_id: string
  total: Cents
  installments: number
}

// --- import and reconciliation (F6) -------------------------------------------

export type ImportFormat = "ofx" | "csv"
/** linked: tied to a bill auto-settle had already paid; nothing was posted. */
export type LineStatus = "pending" | "matched" | "created" | "ignored" | "linked"
/** Why a line of the file was not imported; the rest of the file was. */
export type RejectReason = "invalid_date" | "invalid_amount" | "zero_amount" | "balance_row"

export interface ImportSummary {
  /** Absent when the upload added nothing (the file was imported before). */
  id?: string
  account_id: string
  format: ImportFormat
  created_at: string
  from?: IsoDate
  to?: IsoDate
  /** Lines this upload added. */
  lines: number
  /** Lines an earlier upload already holds. */
  duplicates: number
  rejected_count: number
  rejected: {line: number; reason: RejectReason}[]
  pending: number
}

export interface ImportLine {
  n: number
  date: IsoDate
  /** Signed from the account's side: negative left it. */
  amount: Cents
  description: string
  status: LineStatus
  bill_id?: string
  /** When the line leaves with its import (RFC 3339), 90 days after the upload. */
  expires_at?: string
  /**
   * Pending lines only: open bills this line may settle, closest due date first,
   * then bills auto-settle already paid (status "paid") it may be linked to.
   */
  candidates: Bill[]
}

export interface ImportDetail {
  import: ImportSummary
  lines: ImportLine[]
}

export interface NewImport {
  account_id: string
  format: ImportFormat
  /** The file's bytes, base64. */
  content: string
}

export interface LineResult {
  line: ImportLine
  bill?: Bill
}

export type CsvDelimiter = ";" | "," | "\t"
export type CsvDateFormat = "dd/mm/yyyy" | "yyyy-mm-dd" | "mm/dd/yyyy"

/** How one account's CSV export is read. Columns are 1-based; debit_column 0 = one signed column. */
export interface CsvMapping {
  delimiter: CsvDelimiter
  decimal: "," | "."
  date_format: CsvDateFormat
  skip_rows: number
  date_column: number
  description_column: number
  amount_column: number
  debit_column: number
}

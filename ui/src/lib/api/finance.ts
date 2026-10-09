"use client"

import {toast} from "sonner"

import {t} from "@/lib/i18n"

import {apiClient, isSpaceNotFound} from "@/lib/api/client"
import type {
  Account, Bill, BillPatch, Card, CardPatch, CardStatement, CashFlow, CurrentSpace, Direction, DRE, FinanceSpaces, ListResponse, NewAccount, NewBill,
  NewCard, NewPurchase, NewRecurrence, NewTransfer, Purchase, Occurrence, OpeningBalance, PreviewInput, ProjectionMonth, Recurrence, RecurrenceOccurrences, RecurrencePatch,
  Settings, Settlement, Statement, CsvMapping, ImportDetail, ImportSummary, LineResult, NewImport,
} from "@/lib/api/financeTypes"
import {getSpace, PERSONAL, type Space, setSpace, spaceHeader} from "@/lib/console/space"
import type {Mode} from "@/lib/console/mode"

/**
 * The finance section's calls. Every one carries the mode AND the space, added
 * here and never by a caller: the server reads both from headers (ADR 0011,
 * ADR 0025), so a request that forgot one would be answered about the wrong
 * world. Every write also carries the caller's Idempotency-Key (required by the
 * type, so forgetting it does not compile).
 */
export interface FinanceCtx {
  mode: Mode
  space: Space
}

const BASE = "/v1.0/console/finance"

function headers(c: FinanceCtx, idempotencyKey?: string): Record<string, string> {
  const h: Record<string, string> = {"X-Billing-Mode": c.mode, "X-Billing-Space": spaceHeader(c.space)}
  if (idempotencyKey) h["Idempotency-Key"] = idempotencyKey
  return h
}

/**
 * One place for the one recovery every screen needs: an organization space that
 * answers 404 space-not-found is not (or no longer) this person's, so the
 * selection falls back to personal instead of every block showing an error.
 */
function spaceGone(c: FinanceCtx, error: unknown): never {
  // Only while that space is still the selection: a late 404 from a space the
  // person already left must not overwrite where they went. One toast id, so
  // the blocks failing together show one message.
  if (c.space.kind === "organization" && isSpaceNotFound(error) && spaceHeader(getSpace()) === spaceHeader(c.space)) {
    setSpace(PERSONAL)
    toast.info(t("auth.errors.spaceGone"), {id: "space-gone"})
  }
  throw error
}

async function read<T>(c: FinanceCtx, url: string, params?: Record<string, unknown>, signal?: AbortSignal): Promise<T> {
  try {
    const {data} = await apiClient.request<T>({method: "GET", url: BASE + url, headers: headers(c), params, signal})
    return data
  } catch (e) {
    return spaceGone(c, e)
  }
}

async function write<T>(c: FinanceCtx, method: "POST" | "PATCH" | "PUT", url: string, body: unknown, key: string): Promise<T> {
  try {
    const {data} = await apiClient.request<T>({method, url: BASE + url, headers: headers(c, key), data: body ?? {}})
    return data
  } catch (e) {
    return spaceGone(c, e)
  }
}

/**
 * Query keys. The mode and the space are segments of EVERY key, in that order,
 * which is why this object exists: without them, switching space would render
 * the previous space's rows out of the cache until each query refetched.
 */
export const financeKeys = {
  all: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space)] as const,
  space: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "space"] as const,
  bills: (mode: Mode, space: Space, dir: Direction) => ["finance", mode, spaceHeader(space), "bills", dir] as const,
  bill: (mode: Mode, space: Space, id: string) => ["finance", mode, spaceHeader(space), "bill", id] as const,
  recurrences: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "recurrences"] as const,
  // Under "recurrences", so every write that refreshes the list refreshes the detail.
  recurrenceOccurrences: (mode: Mode, space: Space, id: string) => ["finance", mode, spaceHeader(space), "recurrences", id, "occurrences"] as const,
  projection: (mode: Mode, space: Space, months: number) => ["finance", mode, spaceHeader(space), "projection", months] as const,
  accounts: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "accounts"] as const,
  settings: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "settings"] as const,
  statement: (mode: Mode, space: Space, accountId: string, from: string, to: string) =>
    ["finance", mode, spaceHeader(space), "statement", accountId, from, to] as const,
  cashFlow: (mode: Mode, space: Space, from: string, to: string) => ["finance", mode, spaceHeader(space), "cash-flow", from, to] as const,
  dre: (mode: Mode, space: Space, from: string, to: string) => ["finance", mode, spaceHeader(space), "dre", from, to] as const,
  cards: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "cards"] as const,
  cardStatement: (mode: Mode, space: Space, cardId: string, month: string) =>
    ["finance", mode, spaceHeader(space), "card-statement", cardId, month] as const,
  purchases: (mode: Mode, space: Space, cardId: string) => ["finance", mode, spaceHeader(space), "purchases", cardId] as const,
  imports: (mode: Mode, space: Space, accountId: string) => ["finance", mode, spaceHeader(space), "imports", accountId] as const,
  importDetail: (mode: Mode, space: Space, id: string) => ["finance", mode, spaceHeader(space), "import", id] as const,
  csvMapping: (mode: Mode, space: Space, accountId: string) => ["finance", mode, spaceHeader(space), "csv-mapping", accountId] as const,
  spaces: () => ["finance", "spaces"] as const,
}

// --- spaces -----------------------------------------------------------------

/** The switcher's list. No space header: none is selected yet. */
export async function getFinanceSpaces(): Promise<FinanceSpaces> {
  const {data} = await apiClient.request<FinanceSpaces>({method: "GET", url: BASE + "/spaces", headers: {}})
  return data
}

export const getCurrentSpace = (c: FinanceCtx) => read<CurrentSpace>(c, "/space")

// --- bills --------------------------------------------------------------------

export const listBills = (c: FinanceCtx, direction: Direction, cursor?: string) =>
  read<ListResponse<Bill>>(c, "/bills", {direction, cursor, limit: 100})
export const getBill = (c: FinanceCtx, id: string) => read<Bill>(c, `/bills/${encodeURIComponent(id)}`)
export const createBill = (c: FinanceCtx, body: NewBill, idempotencyKey: string) =>
  write<Bill>(c, "POST", "/bills", body, idempotencyKey)
export const patchBill = (c: FinanceCtx, id: string, body: BillPatch, idempotencyKey: string) =>
  write<Bill>(c, "PATCH", `/bills/${encodeURIComponent(id)}`, body, idempotencyKey)
export const settleBill = (c: FinanceCtx, id: string, body: Settlement, idempotencyKey: string) =>
  write<Bill>(c, "POST", `/bills/${encodeURIComponent(id)}/settle`, body, idempotencyKey)
export const cancelBill = (c: FinanceCtx, id: string, idempotencyKey: string) =>
  write<Bill>(c, "POST", `/bills/${encodeURIComponent(id)}/cancel`, {}, idempotencyKey)
/** Undoes a payment: the bill returns to the open list with auto-settle off. */
export const unsettleBill = (c: FinanceCtx, id: string, idempotencyKey: string) =>
  write<Bill>(c, "POST", `/bills/${encodeURIComponent(id)}/unsettle`, {}, idempotencyKey)

// --- recurrences ----------------------------------------------------------------

export const listRecurrences = (c: FinanceCtx) => read<ListResponse<Recurrence>>(c, "/recurrences")
export const createRecurrence = (c: FinanceCtx, body: NewRecurrence, idempotencyKey: string) =>
  write<Recurrence>(c, "POST", "/recurrences", body, idempotencyKey)
export const patchRecurrence = (c: FinanceCtx, id: string, body: RecurrencePatch, idempotencyKey: string) =>
  write<Recurrence>(c, "PATCH", `/recurrences/${encodeURIComponent(id)}`, body, idempotencyKey)
/** F4's detail: what the recurrence made (each with its state) and what it will make. */
export const getRecurrenceOccurrences = (c: FinanceCtx, id: string) =>
  read<RecurrenceOccurrences>(c, `/recurrences/${encodeURIComponent(id)}/occurrences`)
export const archiveRecurrence = (c: FinanceCtx, id: string, idempotencyKey: string) =>
  write<void>(c, "POST", `/recurrences/${encodeURIComponent(id)}/archive`, {}, idempotencyKey)

/** A read: nothing is stored, so no Idempotency-Key. */
export async function previewRecurrence(c: FinanceCtx, body: PreviewInput, signal?: AbortSignal): Promise<ListResponse<Occurrence>> {
  const {data} = await apiClient.request<ListResponse<Occurrence>>({
    method: "POST", url: BASE + "/recurrences/preview", headers: headers(c), data: body, signal,
  })
  return data
}

export const getProjection = (c: FinanceCtx, months: number) =>
  read<ListResponse<ProjectionMonth>>(c, "/projection", {months})

// --- accounts and settings ------------------------------------------------------

export const listAccounts = (c: FinanceCtx) => read<ListResponse<Account>>(c, "/accounts")
export const createAccount = (c: FinanceCtx, body: NewAccount, idempotencyKey: string) =>
  write<Account>(c, "POST", "/accounts", body, idempotencyKey)
export const archiveAccount = (c: FinanceCtx, id: string, idempotencyKey: string) =>
  write<void>(c, "POST", `/accounts/${encodeURIComponent(id)}/archive`, {}, idempotencyKey)
export const getSettings = (c: FinanceCtx) => read<Settings>(c, "/settings")
export const setDefaultReceivingAccount = (c: FinanceCtx, accountId: string, idempotencyKey: string) =>
  write<Settings>(c, "PUT", "/settings/default-receiving-account", {default_receiving_account_id: accountId}, idempotencyKey)
export const setPostCTechInvoices = (c: FinanceCtx, on: boolean, idempotencyKey: string) =>
  write<Settings>(c, "PUT", "/settings/post-ctech-invoices", {post_ctech_invoices: on}, idempotencyKey)

// --- statement, transfers, reports ------------------------------------------------

/** `to` is exclusive. */
export const getStatement = (c: FinanceCtx, accountId: string, from: string, to: string, signal?: AbortSignal) =>
  read<Statement>(c, `/accounts/${encodeURIComponent(accountId)}/statement`, {from, to}, signal)
export const postOpeningBalance = (c: FinanceCtx, accountId: string, body: OpeningBalance, idempotencyKey: string) =>
  write<{transaction_id: string}>(c, "POST", `/accounts/${encodeURIComponent(accountId)}/opening-balance`, body, idempotencyKey)
export const createTransfer = (c: FinanceCtx, body: NewTransfer, idempotencyKey: string) =>
  write<{transaction_id: string}>(c, "POST", "/transfers", body, idempotencyKey)
/** Only transfers and opening balances; a payment is undone with unsettleBill. */
export const reverseTransaction = (c: FinanceCtx, txId: string, idempotencyKey: string) =>
  write<{transaction_id: string}>(c, "POST", `/transactions/${encodeURIComponent(txId)}/reverse`, {}, idempotencyKey)
/** Months `YYYY-MM`, both inclusive. */
export const getCashFlow = (c: FinanceCtx, from: string, to: string) => read<CashFlow>(c, "/reports/cash-flow", {from, to})
export const getDRE = (c: FinanceCtx, from: string, to: string) => read<DRE>(c, "/reports/dre", {from, to})

// --- cards --------------------------------------------------------------------------

const card = (id: string) => `/cards/${encodeURIComponent(id)}`

export const listCards = (c: FinanceCtx) => read<ListResponse<Card>>(c, "/cards")
export const createCard = (c: FinanceCtx, body: NewCard, idempotencyKey: string) =>
  write<Card>(c, "POST", "/cards", body, idempotencyKey)
export const patchCard = (c: FinanceCtx, id: string, body: CardPatch, idempotencyKey: string) =>
  write<Card>(c, "PATCH", card(id), body, idempotencyKey)
/** `month` is `YYYY-MM`. */
export const getCardStatement = (c: FinanceCtx, cardId: string, month: string) =>
  read<CardStatement>(c, `${card(cardId)}/statements/${encodeURIComponent(month)}`)
export const listPurchases = (c: FinanceCtx, cardId: string) => read<ListResponse<Purchase>>(c, `${card(cardId)}/purchases`)
export const createPurchase = (c: FinanceCtx, cardId: string, body: NewPurchase, idempotencyKey: string) =>
  write<Purchase>(c, "POST", `${card(cardId)}/purchases`, body, idempotencyKey)
export const refundPurchase = (c: FinanceCtx, cardId: string, purchaseId: string, idempotencyKey: string) =>
  write<Purchase>(c, "POST", `${card(cardId)}/purchases/${encodeURIComponent(purchaseId)}/refund`, {}, idempotencyKey)
export const advancePurchase = (c: FinanceCtx, cardId: string, purchaseId: string, idempotencyKey: string) =>
  write<Purchase>(c, "POST", `${card(cardId)}/purchases/${encodeURIComponent(purchaseId)}/advance`, {}, idempotencyKey)
/** Closes the open statement (`month`, `YYYY-MM`) now, before its closing day. A month
 *  that is not the open one is refused, so a repeated click cannot close the next. */
export const closeStatement = (c: FinanceCtx, cardId: string, month: string, idempotencyKey: string) =>
  write<CardStatement>(c, "POST", `${card(cardId)}/close`, {month}, idempotencyKey)

// --- import and reconciliation (F6) ------------------------------------------------

const line = (importId: string, n: number) => `/imports/${encodeURIComponent(importId)}/lines/${n}`

export const listImports = (c: FinanceCtx, accountId: string) => read<ListResponse<ImportSummary>>(c, "/imports", {account_id: accountId})
export const getImport = (c: FinanceCtx, id: string) => read<ImportDetail>(c, `/imports/${encodeURIComponent(id)}`)
/** 201 with an id when lines were added; 200 with no id when the file held nothing new. */
export const uploadImport = (c: FinanceCtx, body: NewImport, idempotencyKey: string) =>
  write<ImportSummary>(c, "POST", "/imports", body, idempotencyKey)
/** Settles the bill with the line's date and amount. */
export const matchLine = (c: FinanceCtx, importId: string, n: number, body: {bill_id: string; difference_category_id?: string}, idempotencyKey: string) =>
  write<LineResult>(c, "POST", `${line(importId, n)}/match`, body, idempotencyKey)
/** Creates a bill from the line and settles it at once. */
export const newFromLine = (c: FinanceCtx, importId: string, n: number, body: {category_id: string; description?: string}, idempotencyKey: string) =>
  write<LineResult>(c, "POST", `${line(importId, n)}/new`, body, idempotencyKey)
/** Ties a pending line to a bill auto-settle already paid: nothing is posted. */
export const linkLine = (c: FinanceCtx, importId: string, n: number, body: {bill_id: string}, idempotencyKey: string) =>
  write<LineResult>(c, "POST", `${line(importId, n)}/link`, body, idempotencyKey)
export const ignoreLine = (c: FinanceCtx, importId: string, n: number, idempotencyKey: string) =>
  write<LineResult>(c, "POST", `${line(importId, n)}/ignore`, {}, idempotencyKey)
export const reopenLine = (c: FinanceCtx, importId: string, n: number, idempotencyKey: string) =>
  write<LineResult>(c, "POST", `${line(importId, n)}/reopen`, {}, idempotencyKey)
export const getCsvMapping = (c: FinanceCtx, accountId: string) => read<CsvMapping>(c, `/accounts/${encodeURIComponent(accountId)}/csv-mapping`)
export const putCsvMapping = (c: FinanceCtx, accountId: string, body: CsvMapping, idempotencyKey: string) =>
  write<CsvMapping>(c, "PUT", `/accounts/${encodeURIComponent(accountId)}/csv-mapping`, body, idempotencyKey)

/**
 * A file's bytes as base64. The bytes are never decoded as text here: a bank's
 * Windows-1252 export read as UTF-8 would lose its accents before the server
 * could tell which charset it is.
 */
export async function fileToBase64(file: Blob): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer())
  let binary = ""
  for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(binary)
}

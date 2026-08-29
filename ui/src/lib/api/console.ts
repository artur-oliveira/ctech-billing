"use client"

import {apiClient} from "@/lib/api/client"
import type {
  ConsoleCustomer,
  ConsoleCustomerDetail,
  ConsoleInvoiceDetail,
  ConsoleInvoiceRow,
  ConsolePage,
  ConsolePrice,
  ConsoleProduct,
  ConsoleOverview,
  ConsoleSession,
  ConsoleSettings,
  ConsoleSubscription,
  ConsoleSubscriptionDetail,
  DocumentLink,
  DunningPolicy,
  DunningStep,
} from "@/lib/api/consoleTypes"
import {getMode, type Mode} from "@/lib/console/mode"

/**
 * The console's calls. Every one of them carries the mode header, and it is
 * added here rather than by each caller: the server reads the tenant from the
 * session and the mode from this header (ADR 0011), so a request that forgets
 * it is a request answered about live data from a screen showing test.
 */
function modeHeaders(mode: Mode = getMode()) {
  return {headers: {"X-Billing-Mode": mode}}
}

/**
 * Query keys. The mode is **part of every key**, which is the whole reason this
 * object exists: without it, switching to test would render live rows out of
 * the cache until each query refetched, and the one mistake this shell is built
 * to prevent is acting on the wrong mode.
 */
export const consoleKeys = {
  session: (mode: Mode) => ["console", mode, "session"] as const,
  overview: (mode: Mode, year: number, month: number) =>
    ["console", mode, "overview", year, month] as const,
  settings: (mode: Mode) => ["console", mode, "settings"] as const,
  invoices: (mode: Mode) => ["console", mode, "invoices"] as const,
  invoiceMonth: (mode: Mode, year: number, month: number) =>
    ["console", mode, "invoices", "month", year, month] as const,
  invoice: (mode: Mode, id: string) => ["console", mode, "invoices", "detail", id] as const,
  customers: (mode: Mode) => ["console", mode, "customers"] as const,
  customer: (mode: Mode, id: string) => ["console", mode, "customers", "detail", id] as const,
  subscriptions: (mode: Mode) => ["console", mode, "subscriptions"] as const,
  subscription: (mode: Mode, id: string) =>
    ["console", mode, "subscriptions", "detail", id] as const,
  products: (mode: Mode) => ["console", mode, "products"] as const,
  product: (mode: Mode, id: string) => ["console", mode, "products", "detail", id] as const,
}

export async function getConsoleSession(mode?: Mode): Promise<ConsoleSession> {
  const {data} = await apiClient.get<ConsoleSession>("/v1.0/console/session", modeHeaders(mode))
  return data
}

export async function getConsoleOverview(
  year: number,
  month: number,
  mode?: Mode,
): Promise<ConsoleOverview> {
  const {data} = await apiClient.get<ConsoleOverview>("/v1.0/console/overview", {
    ...modeHeaders(mode),
    params: {year, month},
  })
  return data
}

export async function getConsoleSettings(mode?: Mode): Promise<ConsoleSettings> {
  const {data} = await apiClient.get<ConsoleSettings>("/v1.0/console/settings", modeHeaders(mode))
  return data
}

/** Replaces the organization's default schedule. An empty list restores the
 *  built-in one — there is no "never chase this". */
export async function setDunningPolicy(
  steps: DunningStep[],
  mode?: Mode,
): Promise<DunningPolicy> {
  const {data} = await apiClient.put<DunningPolicy>(
    "/v1.0/console/settings/dunning",
    {steps},
    modeHeaders(mode),
  )
  return data
}

/** Overrides (or clears) one product's schedule. */
export async function setProductDunningPolicy(
  productId: string,
  steps: DunningStep[],
  mode?: Mode,
): Promise<DunningPolicy> {
  const {data} = await apiClient.put<DunningPolicy>(
    `/v1.0/console/products/${productId}/dunning`,
    {steps},
    modeHeaders(mode),
  )
  return data
}

/** Reveals a customer's full tax id, and records who looked. A POST because it
 *  has an effect — the audit row — not because it sends anything. */
export async function revealTaxID(customerId: string, mode?: Mode): Promise<string> {
  const {data} = await apiClient.post<{tax_id: string}>(
    `/v1.0/console/customers/${customerId}/tax-id`,
    undefined,
    modeHeaders(mode),
  )
  return data.tax_id
}

export async function listConsoleInvoices(
  year: number,
  month: number,
  cursor?: string,
  mode?: Mode,
): Promise<ConsolePage<ConsoleInvoiceRow>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleInvoiceRow>>("/v1.0/console/invoices", {
    ...modeHeaders(mode),
    params: {year, month, ...(cursor ? {cursor} : {})},
  })
  return data
}

export async function getConsoleInvoice(id: string, mode?: Mode): Promise<ConsoleInvoiceDetail> {
  const {data} = await apiClient.get<ConsoleInvoiceDetail>(
    `/v1.0/console/invoices/${id}`,
    modeHeaders(mode),
  )
  return data
}

export async function listConsoleCustomers(mode?: Mode): Promise<ConsolePage<ConsoleCustomer>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleCustomer>>(
    "/v1.0/console/customers",
    modeHeaders(mode),
  )
  return data
}

export async function getConsoleCustomer(id: string, mode?: Mode): Promise<ConsoleCustomerDetail> {
  const {data} = await apiClient.get<ConsoleCustomerDetail>(
    `/v1.0/console/customers/${id}`,
    modeHeaders(mode),
  )
  return data
}

export async function listConsoleSubscriptions(
  mode?: Mode,
): Promise<ConsolePage<ConsoleSubscription>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleSubscription>>(
    "/v1.0/console/subscriptions",
    modeHeaders(mode),
  )
  return data
}

export async function getConsoleSubscription(
  id: string,
  mode?: Mode,
): Promise<ConsoleSubscriptionDetail> {
  const {data} = await apiClient.get<ConsoleSubscriptionDetail>(
    `/v1.0/console/subscriptions/${id}`,
    modeHeaders(mode),
  )
  return data
}

/** Immediate or at-period-end. Two different operations, never a checkbox: one
 *  stops entitlement now, the other keeps it until the period the customer has
 *  already paid for runs out. */
export async function cancelConsoleSubscription(
  id: string,
  atPeriodEnd: boolean,
  mode?: Mode,
): Promise<ConsoleSubscription> {
  const {data} = await apiClient.post<ConsoleSubscription>(
    `/v1.0/console/subscriptions/${id}/cancel`,
    {at_period_end: atPeriodEnd},
    modeHeaders(mode),
  )
  return data
}

export async function listConsoleProducts(mode?: Mode): Promise<ConsolePage<ConsoleProduct>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleProduct>>(
    "/v1.0/console/products",
    modeHeaders(mode),
  )
  return data
}

export async function getConsoleProduct(id: string, mode?: Mode): Promise<ConsoleProduct> {
  const {data} = await apiClient.get<ConsoleProduct>(
    `/v1.0/console/products/${id}`,
    modeHeaders(mode),
  )
  return data
}

export interface NewProductInput {
  name: string
  owner_key?: string
}

export async function createProduct(input: NewProductInput, mode?: Mode): Promise<ConsoleProduct> {
  const {data} = await apiClient.post<ConsoleProduct>(
    "/v1.0/console/products",
    input,
    modeHeaders(mode),
  )
  return data
}

export interface NewPriceInput {
  product_id: string
  type: "fixed" | "metered"
  unit_amount: number
  recurrence: {interval: string; count: number}
  billing_timing: "advance" | "arrears"
}

/** A new price, never an edit: a price is immutable, and the screen says so. */
export async function createPrice(input: NewPriceInput, mode?: Mode): Promise<ConsolePrice> {
  const {data} = await apiClient.post<ConsolePrice>(
    "/v1.0/console/prices",
    input,
    modeHeaders(mode),
  )
  return data
}

/** Withdraws a price from sale. It does not touch the subscriptions on it. */
export async function archivePrice(id: string, mode?: Mode): Promise<ConsolePrice> {
  const {data} = await apiClient.post<ConsolePrice>(
    `/v1.0/console/prices/${id}/archive`,
    undefined,
    modeHeaders(mode),
  )
  return data
}

export async function getConsoleInvoicePDF(id: string, mode?: Mode): Promise<DocumentLink> {
  const {data} = await apiClient.get<DocumentLink>(
    `/v1.0/console/invoices/${id}/pdf`,
    modeHeaders(mode),
  )
  return data
}

export interface IssuerInput {
  legal_name: string
  tax_id: string
  address: string
  email: string
}

/** Who the invoice PDF says is charging. Written as one block: the four fields
 *  are printed together, and a partial update is how a document ends up headed
 *  by one company's name over another's CNPJ. */
export async function setIssuer(input: IssuerInput, mode?: Mode): Promise<IssuerInput> {
  const {data} = await apiClient.put<IssuerInput>(
    "/v1.0/console/settings/issuer",
    input,
    modeHeaders(mode),
  )
  return data
}

/** Issues a draft invoice — the sweep's own path, with the operator as actor. */
export async function finalizeInvoice(id: string, mode?: Mode): Promise<ConsoleInvoiceDetail> {
  const {data} = await apiClient.post<ConsoleInvoiceDetail>(
    `/v1.0/console/invoices/${id}/finalize`,
    undefined,
    modeHeaders(mode),
  )
  return data
}

export async function voidInvoice(id: string, mode?: Mode): Promise<ConsoleInvoiceDetail> {
  const {data} = await apiClient.post<ConsoleInvoiceDetail>(
    `/v1.0/console/invoices/${id}/void`,
    undefined,
    modeHeaders(mode),
  )
  return data
}

export interface CreditNoteInput {
  amount: number
  reason: string
  refunded_externally: boolean
  external_refund_ref?: string
}

export async function creditInvoice(
  id: string,
  input: CreditNoteInput,
  mode?: Mode,
): Promise<void> {
  await apiClient.post(`/v1.0/console/invoices/${id}/credit-notes`, input, modeHeaders(mode))
}

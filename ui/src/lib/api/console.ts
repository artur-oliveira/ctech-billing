"use client";

import {apiClient, problemCode} from "@/lib/api/client";
import {currentLocale} from "@/lib/i18n";
import type {SupportedLocale} from "@/lib/locale";
import type {
  BillingPlanState,
  ApplicationStatus,
  ApplyRequest,
  ConsoleCredential,
  ConsoleCredentialList,
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
  ConsoleSubscriptionRow,
  ConsoleWebhookList,
  ConsoleWebhookWithSecret,
  DocumentLink,
  DunningPolicy,
  DunningStep,
  ExcessPayment,
  MerchantApplication,
  MerchantCompany,
  MerchantCompanyList,
} from "@/lib/api/consoleTypes";

import type {ConsoleScope} from "@/lib/console/merchant";

/**
 * The billing half's calls. Every one carries the mode AND the organization,
 * added here rather than by each caller: the server resolves the tenant from
 * the selected organization and the person's role (ADR 0028) and the mode from
 * the header (ADR 0011). A scope is required, never read from storage here.
 */
function scopeHeaders(s: ConsoleScope) {
  return {headers: {"X-Billing-Mode": s.mode, "X-Billing-Space": `org:${s.org}`}};
}

/**
 * Query keys. Organization and mode are part of every key: two organizations
 * or two modes are two caches, so a switch can never show one's rows under the
 * other's name.
 */
export const consoleKeys = {
  session: () => ["console", "session"] as const,
  overview: (s: ConsoleScope, year: number, month: number) =>
    ["console", s.org, s.mode, "overview", year, month] as const,
  settings: (s: ConsoleScope) => ["console", s.org, s.mode, "settings"] as const,
  plan: (s: ConsoleScope) => ["console", s.org, s.mode, "plan"] as const,
  credentials: (s: ConsoleScope) => ["console", s.org, s.mode, "credentials"] as const,
  webhooks: (s: ConsoleScope) => ["console", s.org, s.mode, "webhooks"] as const,
  invoices: (s: ConsoleScope) => ["console", s.org, s.mode, "invoices"] as const,
  invoiceMonth: (s: ConsoleScope, year: number, month: number) =>
    ["console", s.org, s.mode, "invoices", "month", year, month] as const,
  invoice: (s: ConsoleScope, id: string) => ["console", s.org, s.mode, "invoices", "detail", id] as const,
  customers: (s: ConsoleScope) => ["console", s.org, s.mode, "customers"] as const,
  customer: (s: ConsoleScope, id: string) => ["console", s.org, s.mode, "customers", "detail", id] as const,
  subscriptions: (s: ConsoleScope) => ["console", s.org, s.mode, "subscriptions"] as const,
  subscription: (s: ConsoleScope, id: string) =>
    ["console", s.org, s.mode, "subscriptions", "detail", id] as const,
  products: (s: ConsoleScope) => ["console", s.org, s.mode, "products"] as const,
  product: (s: ConsoleScope, id: string) => ["console", s.org, s.mode, "products", "detail", id] as const,
  /** The application is not a mode's: no mode in the key (spec § 4.1). */
  merchantApplication: (s: ConsoleScope) => ["console", s.org, "merchant", "application"] as const,
  merchantCompanies: (s: ConsoleScope) => ["console", s.org, "merchant", "companies"] as const,
  applications: (s: ConsoleScope, status: ApplicationStatus) => ["console", s.org, s.mode, "applications", status] as const,
};

/** The organizations this person may run billing for. No mode, no space: it is
 *  what an organization is chosen from. */
export async function getConsoleSession(): Promise<ConsoleSession> {
  const {data} = await apiClient.get<ConsoleSession>("/v1.0/console/session");
  return data;
}

export async function getConsoleOverview(
  scope: ConsoleScope,
  year: number,
  month: number,
): Promise<ConsoleOverview> {
  const {data} = await apiClient.get<ConsoleOverview>("/v1.0/console/overview", {
    ...scopeHeaders(scope),
    params: {year, month},
  });
  return data;
}

export async function getConsoleSettings(scope: ConsoleScope): Promise<ConsoleSettings> {
  const {data} = await apiClient.get<ConsoleSettings>("/v1.0/console/settings", scopeHeaders(scope));
  return data;
}

/** Replaces the organization's default schedule. An empty list restores the
 *  built-in one — there is no "never chase this". */
export async function setDunningPolicy(
  scope: ConsoleScope,
  steps: DunningStep[],
): Promise<DunningPolicy> {
  const {data} = await apiClient.put<DunningPolicy>(
    "/v1.0/console/settings/dunning",
    {steps},
    scopeHeaders(scope),
  );
  return data;
}

/** Overrides (or clears) one product's schedule. */
export async function setProductDunningPolicy(
  scope: ConsoleScope,
  productId: string,
  steps: DunningStep[],
): Promise<DunningPolicy> {
  const {data} = await apiClient.put<DunningPolicy>(
    `/v1.0/console/products/${productId}/dunning`,
    {steps},
    scopeHeaders(scope),
  );
  return data;
}

/** Reveals a customer's full tax id, and records who looked. A POST because it
 *  has an effect — the audit row — not because it sends anything. */
export async function revealTaxID(scope: ConsoleScope, customerId: string): Promise<string> {
  const {data} = await apiClient.post<{tax_id: string}>(
    `/v1.0/console/customers/${customerId}/tax-id`,
    undefined,
    scopeHeaders(scope),
  );
  return data.tax_id;
}

export async function listConsoleInvoices(
  scope: ConsoleScope,
  year: number,
  month: number,
  cursor?: string,
): Promise<ConsolePage<ConsoleInvoiceRow>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleInvoiceRow>>("/v1.0/console/invoices", {
    ...scopeHeaders(scope),
    params: {year, month, ...(cursor ? {cursor} : {})},
  });
  return data;
}

export async function getConsoleInvoice(scope: ConsoleScope, id: string): Promise<ConsoleInvoiceDetail> {
  const {data} = await apiClient.get<ConsoleInvoiceDetail>(
    `/v1.0/console/invoices/${id}`,
    scopeHeaders(scope),
  );
  return data;
}

export async function listConsoleCustomers(scope: ConsoleScope): Promise<ConsolePage<ConsoleCustomer>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleCustomer>>(
    "/v1.0/console/customers",
    scopeHeaders(scope),
  );
  return data;
}

export async function getConsoleCustomer(scope: ConsoleScope, id: string): Promise<ConsoleCustomerDetail> {
  const {data} = await apiClient.get<ConsoleCustomerDetail>(
    `/v1.0/console/customers/${id}`,
    scopeHeaders(scope),
  );
  return data;
}

export async function listConsoleSubscriptions(
  scope: ConsoleScope,

): Promise<ConsolePage<ConsoleSubscriptionRow>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleSubscriptionRow>>(
    "/v1.0/console/subscriptions",
    scopeHeaders(scope),
  );
  return data;
}

export async function getConsoleSubscription(
  scope: ConsoleScope,
  id: string,
): Promise<ConsoleSubscriptionDetail> {
  const {data} = await apiClient.get<ConsoleSubscriptionDetail>(
    `/v1.0/console/subscriptions/${id}`,
    scopeHeaders(scope),
  );
  return data;
}

/** Immediate or at-period-end. Two different operations, never a checkbox: one
 *  stops entitlement now, the other keeps it until the period the customer has
 *  already paid for runs out. */
export async function cancelConsoleSubscription(
  scope: ConsoleScope,
  id: string,
  atPeriodEnd: boolean,
): Promise<ConsoleSubscription> {
  const {data} = await apiClient.post<ConsoleSubscription>(
    `/v1.0/console/subscriptions/${id}/cancel`,
    {at_period_end: atPeriodEnd},
    scopeHeaders(scope),
  );
  return data;
}

export async function listConsoleProducts(scope: ConsoleScope): Promise<ConsolePage<ConsoleProduct>> {
  const {data} = await apiClient.get<ConsolePage<ConsoleProduct>>(
    "/v1.0/console/products",
    scopeHeaders(scope),
  );
  return data;
}

export async function getConsoleProduct(scope: ConsoleScope, id: string): Promise<ConsoleProduct> {
  const {data} = await apiClient.get<ConsoleProduct>(
    `/v1.0/console/products/${id}`,
    scopeHeaders(scope),
  );
  return data;
}

export interface NewProductInput {
  name: string
  owner_key?: string
  description?: string
  url?: string
}

/** Replaces a product's description and URL; empty clears. */
export async function setProductDetails(
  scope: ConsoleScope,
  productId: string,
  details: {description: string; url: string},
): Promise<ConsoleProduct> {
  const {data} = await apiClient.put<ConsoleProduct>(
    `/v1.0/console/products/${productId}/details`,
    details,
    scopeHeaders(scope),
  );
  return data;
}

export async function createProduct(scope: ConsoleScope, input: NewProductInput): Promise<ConsoleProduct> {
  const {data} = await apiClient.post<ConsoleProduct>(
    "/v1.0/console/products",
    input,
    scopeHeaders(scope),
  );
  return data;
}

export interface NewPriceInput {
  product_id: string
  type: "fixed" | "metered"
  unit_amount: number
  recurrence: {interval: string; count: number}
  billing_timing: "advance" | "arrears"
  name?: string
  metadata?: Record<string, string>
}

/** A new price, never an edit: a price is immutable, and the screen says so. */
export async function createPrice(scope: ConsoleScope, input: NewPriceInput): Promise<ConsolePrice> {
  const {data} = await apiClient.post<ConsolePrice>(
    "/v1.0/console/prices",
    input,
    scopeHeaders(scope),
  );
  return data;
}

/** Withdraws a price from sale. It does not touch the subscriptions on it. */
export async function archivePrice(scope: ConsoleScope, id: string): Promise<ConsolePrice> {
  const {data} = await apiClient.post<ConsolePrice>(
    `/v1.0/console/prices/${id}/archive`,
    undefined,
    scopeHeaders(scope),
  );
  return data;
}

export async function getConsoleInvoicePDF(
  scope: ConsoleScope,
  id: string,
  lang: SupportedLocale = currentLocale(),
): Promise<DocumentLink> {
  const {data} = await apiClient.get<DocumentLink>(
    `/v1.0/console/invoices/${id}/pdf`,
    {...scopeHeaders(scope), params: {lang}},
  );
  return data;
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
export async function setIssuer(scope: ConsoleScope, input: IssuerInput): Promise<IssuerInput> {
  const {data} = await apiClient.put<IssuerInput>(
    "/v1.0/console/settings/issuer",
    input,
    scopeHeaders(scope),
  );
  return data;
}

/** Issues a draft invoice — the sweep's own path, with the operator as actor. */
export async function finalizeInvoice(scope: ConsoleScope, id: string): Promise<ConsoleInvoiceDetail> {
  const {data} = await apiClient.post<ConsoleInvoiceDetail>(
    `/v1.0/console/invoices/${id}/finalize`,
    undefined,
    scopeHeaders(scope),
  );
  return data;
}

export async function voidInvoice(scope: ConsoleScope, id: string): Promise<ConsoleInvoiceDetail> {
  const {data} = await apiClient.post<ConsoleInvoiceDetail>(
    `/v1.0/console/invoices/${id}/void`,
    undefined,
    scopeHeaders(scope),
  );
  return data;
}

export interface CreditNoteInput {
  amount: number
  reason: string
  refunded_externally: boolean
  external_refund_ref?: string
  /** BYOK: give the amount back from the invoice's Pix (spec § 7.5). */
  refund_via_pix?: boolean
}

export async function creditInvoice(
  scope: ConsoleScope,
  id: string,
  input: CreditNoteInput,
): Promise<void> {
  await apiClient.post(`/v1.0/console/invoices/${id}/credit-notes`, input, scopeHeaders(scope));
}

/** The selected organization's current application; null when it never applied. */
export async function getMerchantApplication(scope: ConsoleScope): Promise<MerchantApplication | null> {
  try {
    const {data} = await apiClient.get<MerchantApplication>("/v1.0/console/merchant/application", scopeHeaders(scope));
    return data;
  } catch (error) {
    if (problemCode(error) === "no_application") return null;
    throw error;
  }
}

export async function applyForMerchant(scope: ConsoleScope, body: ApplyRequest): Promise<MerchantApplication> {
  const {data} = await apiClient.post<MerchantApplication>("/v1.0/console/merchant/application", body, scopeHeaders(scope));
  return data;
}

/** The organization's companies for the form; `truncated` when ctech-account cut the list at 200. */
export async function listMerchantCompanies(scope: ConsoleScope): Promise<MerchantCompanyList> {
  const {data} = await apiClient.get<{data: MerchantCompany[]; truncated?: boolean}>(
    "/v1.0/console/merchant/companies", scopeHeaders(scope));
  return {companies: data.data ?? [], truncated: data.truncated === true};
}

export async function listApplications(scope: ConsoleScope, status: ApplicationStatus, cursor?: string): Promise<ConsolePage<MerchantApplication>> {
  const {data} = await apiClient.get<ConsolePage<MerchantApplication>>("/v1.0/console/applications", {
    ...scopeHeaders(scope), params: {status, ...(cursor ? {cursor} : {})},
  });
  return data;
}

export async function approveApplication(scope: ConsoleScope, id: string): Promise<MerchantApplication> {
  const {data} = await apiClient.post<MerchantApplication>(`/v1.0/console/applications/${encodeURIComponent(id)}/approve`, {}, scopeHeaders(scope));
  return data;
}

export async function rejectApplication(scope: ConsoleScope, id: string, reason: string): Promise<MerchantApplication> {
  const {data} = await apiClient.post<MerchantApplication>(`/v1.0/console/applications/${encodeURIComponent(id)}/reject`, {reason}, scopeHeaders(scope));
  return data;
}

/** The merchant's API credentials: their own ctech-account OAuth clients (spec § 5.1). */
export async function listConsoleCredentials(scope: ConsoleScope): Promise<ConsoleCredentialList> {
  const {data} = await apiClient.get<ConsoleCredentialList>("/v1.0/console/credentials", scopeHeaders(scope));
  return data;
}

export async function connectCredential(scope: ConsoleScope, clientId: string): Promise<ConsoleCredential> {
  const {data} = await apiClient.post<ConsoleCredential>("/v1.0/console/credentials", {client_id: clientId}, scopeHeaders(scope));
  return data;
}

export async function disconnectCredential(scope: ConsoleScope, clientId: string): Promise<void> {
  await apiClient.delete(`/v1.0/console/credentials/${encodeURIComponent(clientId)}`, scopeHeaders(scope));
}

export async function listConsoleWebhooks(scope: ConsoleScope): Promise<ConsoleWebhookList> {
  const {data} = await apiClient.get<ConsoleWebhookList>("/v1.0/console/webhooks", scopeHeaders(scope));
  return data;
}

export async function createWebhook(scope: ConsoleScope, body: {url: string; events: string[]}): Promise<ConsoleWebhookWithSecret> {
  const {data} = await apiClient.post<ConsoleWebhookWithSecret>("/v1.0/console/webhooks", body, scopeHeaders(scope));
  return data;
}

export async function deleteWebhook(scope: ConsoleScope, id: string): Promise<void> {
  await apiClient.delete(`/v1.0/console/webhooks/${encodeURIComponent(id)}`, scopeHeaders(scope));
}

export async function rotateWebhookSecret(scope: ConsoleScope, id: string): Promise<ConsoleWebhookWithSecret> {
  const {data} = await apiClient.post<ConsoleWebhookWithSecret>(
    `/v1.0/console/webhooks/${encodeURIComponent(id)}/rotate-secret`, {}, scopeHeaders(scope));
  return data;
}

export async function testWebhook(scope: ConsoleScope, id: string): Promise<{event_id: string}> {
  const {data} = await apiClient.post<{event_id: string}>(
    `/v1.0/console/webhooks/${encodeURIComponent(id)}/test`, {}, scopeHeaders(scope));
  return data;
}

/** The BYOK plan screen: test mode reads the live plan. */
export async function getBillingPlan(scope: ConsoleScope): Promise<BillingPlanState> {
  const {data} = await apiClient.get<BillingPlanState>("/v1.0/console/plan", scopeHeaders(scope));
  return data;
}

/** Moves the organization onto a public plan (live only); answers the new state. */
export async function changeBillingPlan(scope: ConsoleScope, plan: string): Promise<BillingPlanState> {
  const {data} = await apiClient.post<BillingPlanState>("/v1.0/console/plan", {plan}, scopeHeaders(scope));
  return data;
}

/** "Gerar novo Pix": a new BYOK charge after the previous one ended unpaid. */
export async function newInvoicePix(scope: ConsoleScope, id: string): Promise<ConsoleInvoiceDetail> {
  const {data} = await apiClient.post<ConsoleInvoiceDetail>(`/v1.0/console/invoices/${id}/charge`, {}, scopeHeaders(scope));
  return data;
}

/** Test mode only: pays the Pix in the provider's sandbox; the confirmation
 *  then arrives through the real path, so the screen refetches. */
export async function simulateInvoicePayment(scope: ConsoleScope, id: string): Promise<void> {
  await apiClient.post(`/v1.0/console/invoices/${id}/simulate-payment`, {}, scopeHeaders(scope));
}

/** "Devolver": the merchant's explicit act (D19). */
export async function refundExcessPayment(scope: ConsoleScope, id: string): Promise<ExcessPayment> {
  const {data} = await apiClient.post<ExcessPayment>(`/v1.0/console/excess-payments/${id}/refund`, {}, scopeHeaders(scope));
  return data;
}

/** Turns the provider on for collection with its late fees, as percentages
 *  with two decimals (the server stores basis points). */
export async function setCollection(
  scope: ConsoleScope,
  body: {provider: string; late_fee_percent: number; monthly_interest_percent: number},
): Promise<ConsoleSettings> {
  const {data} = await apiClient.put<ConsoleSettings>("/v1.0/console/collection", body, scopeHeaders(scope));
  return data;
}

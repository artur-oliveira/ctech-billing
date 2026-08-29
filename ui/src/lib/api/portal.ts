"use client"

import {apiClient} from "@/lib/api/client"
import type {
  Invoice,
  ListResponse,
  PaymentResult,
  Session,
  Subscription,
} from "@/lib/api/types"

/**
 * Query keys, in one place. A key spelled two ways is a cache that never
 * invalidates — after a payment starts, the invoice detail has to be the same
 * key the payment mutation writes into.
 *
 * The inverse is just as costly: one key holding two *shapes*. `invoices` was
 * once the key of both P1's `useQuery` (a `ListResponse`) and P2's
 * `useInfiniteQuery` (a `{pages, pageParams}`), so opening the list after the
 * home screen had populated the cache handed TanStack a page object with no
 * `pages` array and threw before the first render. Each shape gets its own
 * leaf; `invoices` is a prefix and nothing else.
 */
export const portalKeys = {
  session: ["portal", "session"] as const,
  /** Prefix only — never a query's own key. Invalidating it catches every
   *  list shape and every detail below it. */
  invoices: ["portal", "invoices"] as const,
  /** One page, for composing the home screen. */
  invoiceList: ["portal", "invoices", "list"] as const,
  /** Cursor pages, for the list screen. */
  invoicePages: ["portal", "invoices", "pages"] as const,
  invoice: (id: string) => ["portal", "invoices", "detail", id] as const,
  subscriptions: ["portal", "subscriptions"] as const,
  subscription: (id: string) => ["portal", "subscriptions", "detail", id] as const,
}

/**
 * Is the API answering at all?
 *
 * Unauthenticated on purpose. /maintenance uses this, and probing a portal
 * route there would answer 401 for anybody whose session ended while the
 * service was down — which the client turns into a redirect to /login, which
 * cannot load either. The health route is the one endpoint whose answer is
 * about the service and not the caller.
 */
export async function getHealth(): Promise<true> {
  // Liveness, not /v1.0/health-check. Two reasons, and either alone settles it:
  //
  //   - The report answers 503 whenever a dependency fails, so a browser parked
  //     on /maintenance would be told the service is still down by an endpoint
  //     that just answered it. This asks "is the API answering", and liveness is
  //     the endpoint that answers exactly that.
  //   - This is polled, by every browser sitting on that page. The report costs
  //     a DescribeTable, a Valkey PING and two /proc reads per call; liveness
  //     costs a clock read.
  await apiClient.get("/v1.0/health")
  // Not `void`. TanStack Query rejects an `undefined` result outright — the
  // query lands in an error state with "Query data cannot be undefined" and
  // never reports success, which for the one caller here means a maintenance
  // screen that probes correctly and never lets anybody off it.
  return true
}

export async function getSession(): Promise<Session> {
  const {data} = await apiClient.get<Session>("/v1.0/portal/session")
  return data
}

/**
 * Records agreement to the billing terms addendum.
 *
 * No body: the version is the server's. A client that could name one could
 * accept a document it chose rather than the one in force.
 *
 * It returns the session so the caller writes the fresh one straight into the
 * query cache instead of refetching — the gate is dismissed by that value
 * changing, and a round trip between the click and the dismissal is a modal
 * that visibly hesitates.
 */
export async function acceptTerms(): Promise<Session> {
  const {data} = await apiClient.post<Session>("/v1.0/portal/terms/accept")
  return data
}

export async function listInvoices(cursor?: string): Promise<ListResponse<Invoice>> {
  const {data} = await apiClient.get<ListResponse<Invoice>>("/v1.0/portal/invoices", {
    params: cursor ? {cursor} : undefined,
  })
  return data
}

export async function getInvoice(id: string): Promise<Invoice> {
  const {data} = await apiClient.get<Invoice>(`/v1.0/portal/invoices/${id}`)
  return data
}

/** Opens (or re-opens) the PIX charge. The body is empty by design — the
 *  server decides everything from the session and the invoice. */
export async function payInvoice(id: string): Promise<PaymentResult> {
  const {data} = await apiClient.post<PaymentResult>(`/v1.0/portal/invoices/${id}/pay`)
  return data
}

export async function listSubscriptions(): Promise<ListResponse<Subscription>> {
  const {data} = await apiClient.get<ListResponse<Subscription>>("/v1.0/portal/subscriptions")
  return data
}

/** The detail, which is the list row plus the plan's own invoice history. */
export async function getSubscription(id: string): Promise<Subscription> {
  const {data} = await apiClient.get<Subscription>(`/v1.0/portal/subscriptions/${id}`)
  return data
}

export async function cancelSubscription(id: string): Promise<Subscription> {
  const {data} = await apiClient.post<Subscription>(`/v1.0/portal/subscriptions/${id}/cancel`)
  return data
}

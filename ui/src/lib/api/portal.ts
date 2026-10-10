"use client"

import type {AxiosRequestConfig} from "axios"
import {toast} from "sonner"

import {apiClient, isSpaceNotFound} from "@/lib/api/client"
import {spaceHeader, PERSONAL, type Space} from "@/lib/console/space"
import {currentLocale, t} from "@/lib/i18n"
import {getPortalSpace, setPortalSpace} from "@/lib/portal/space"
import type {SupportedLocale} from "@/lib/locale"
import type {
  DocumentLink,
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
  get session() { return [...scope(), "session"] as const },
  /** Prefix only — never a query's own key. Invalidating it catches every
   *  list shape and every detail below it. */
  get invoices() { return [...scope(), "invoices"] as const },
  /** One page, for composing the home screen. */
  get invoiceList() { return [...scope(), "invoices", "list"] as const },
  /** Cursor pages, for the list screen. */
  get invoicePages() { return [...scope(), "invoices", "pages"] as const },
  invoice: (id: string) => [...scope(), "invoices", "detail", id] as const,
  get subscriptions() { return [...scope(), "subscriptions"] as const },
  subscription: (id: string) => [...scope(), "subscriptions", "detail", id] as const,
  /** Not keyed by the space: it is the list the switch is drawn from, the same
   *  whichever space is selected. */
  spaces: ["portal-spaces"] as const,
}

/** Every portal key starts with the selection it was read for, like
 *  financeKeys: two spaces are two caches, so a change of selection from
 *  anywhere can never serve one space's bills under the other's name. */
function scope() {
  return ["portal", spaceHeader(getPortalSpace())] as const
}

/** The portal's selector header, read at call time (ADR 0025). */
export function portalSpaceHeaders(space: Space = getPortalSpace()): Record<string, string> {
  return {"X-Billing-Space": spaceHeader(space)}
}

/**
 * One portal call, sent for the space selected when it started. A 404
 * space-not-found means that organization is no longer this person's to
 * manage: fall back to Pessoal, once, and only while it is still the
 * selection — a late answer from a space already left must not move the
 * person back. One toast id, so the calls failing together say it once.
 */
async function portal<T>(config: AxiosRequestConfig): Promise<T> {
  const space = getPortalSpace()
  try {
    const {data} = await apiClient.request<T>({
      ...config,
      headers: {...(config.headers as Record<string, string> | undefined), ...portalSpaceHeaders(space)},
    })
    return data
  } catch (error) {
    if (space.kind === "organization" && isSpaceNotFound(error) && spaceHeader(getPortalSpace()) === spaceHeader(space)) {
      setPortalSpace(PERSONAL)
      toast.info(t("portal.space.lost"), {id: "portal-space-lost"})
    }
    throw error
  }
}

export interface PortalSpace {
  selector: string
  display_name: string
  role?: string
}

export interface PortalSpaces {
  spaces: PortalSpace[]
  organizations_unavailable: boolean
}

/** Pessoal, then the organizations this person owns or administers. Information
 *  only: every other call is re-authorized against the selection it carries. */
export async function listPortalSpaces(): Promise<PortalSpaces> {
  const {data} = await apiClient.get<PortalSpaces>("/v1.0/portal/spaces")
  return data
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
  return portal<Session>({method: "GET", url: "/v1.0/portal/session"})
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
  return portal<Session>({method: "POST", url: "/v1.0/portal/terms/accept"})
}

export async function listInvoices(cursor?: string): Promise<ListResponse<Invoice>> {
  return portal<ListResponse<Invoice>>({
    method: "GET",
    url: "/v1.0/portal/invoices",
    params: cursor ? {cursor} : undefined,
  })
}

export async function getInvoice(id: string): Promise<Invoice> {
  return portal<Invoice>({method: "GET", url: `/v1.0/portal/invoices/${id}`})
}

/**
 * A signed link to the invoice's PDF.
 *
 * The server renders and stores the document on the first request, so the first
 * call for an invoice is slower than the rest. Not prefetched on render: most
 * readers open an invoice to pay it, not to file it, and rendering a PDF for
 * everybody who looks would be work nobody asked for.
 */
export async function getInvoicePDF(id: string, lang: SupportedLocale = currentLocale()): Promise<DocumentLink> {
  return portal<DocumentLink>({method: "GET", url: `/v1.0/portal/invoices/${id}/pdf`, params: {lang}})
}

/** Opens (or re-opens) the PIX charge. The body is empty by design — the
 *  server decides everything from the session and the invoice. */
export async function payInvoice(id: string): Promise<PaymentResult> {
  return portal<PaymentResult>({method: "POST", url: `/v1.0/portal/invoices/${id}/pay`})
}

export async function listSubscriptions(): Promise<ListResponse<Subscription>> {
  return portal<ListResponse<Subscription>>({method: "GET", url: "/v1.0/portal/subscriptions"})
}

/** The detail, which is the list row plus the plan's own invoice history. */
export async function getSubscription(id: string): Promise<Subscription> {
  return portal<Subscription>({method: "GET", url: `/v1.0/portal/subscriptions/${id}`})
}

export async function cancelSubscription(id: string): Promise<Subscription> {
  return portal<Subscription>({method: "POST", url: `/v1.0/portal/subscriptions/${id}/cancel`})
}

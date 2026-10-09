"use client"

import axios, {type AxiosError, type InternalAxiosRequestConfig} from "axios"

import {codeMessage, fieldErrorMessage, type FieldError} from "@/lib/errors"
import {t} from "@/lib/i18n"
import {USE_MOCK} from "@/lib/mockConfig"

/**
 * RFC 7807, as ctech-go-common/problem emits it. `title`, `detail` and every
 * `message` are English fallbacks for logs: what a reader sees is looked up by
 * `code` (and `errors[].code`) in the `errors` catalog.
 */
export interface Problem {
  type: string
  title: string
  status: number
  detail?: string
  code?: string
  errors?: FieldError[]
}

let accessToken: string | null = null
export const setAccessToken = (t: string | null) => {
  accessToken = t
}
export const getAccessToken = () => accessToken

/**
 * Where the API lives.
 *
 * Absolute in every deployed environment: the pages are served from
 * `billing[-env].aoctech.app` and call `billing-api[-env].aoctech.app`
 * directly, rather than being proxied back through their own CloudFront
 * distribution to Cloudflare and on to HAProxy — three hops and three TLS
 * terminations for a request with one destination.
 *
 * Empty falls back to same-origin, which is what `next dev` uses (its rewrite
 * proxies /v1.0/* to the local API) and what a rollback sets.
 *
 * Exported because `fetch` cannot read axios' baseURL, and the settlement
 * stream is a `fetch` — EventSource cannot send an Authorization header.
 */
export const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || ""

export const apiClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 10_000,
  // Mock mode replaces the transport, not the calling code. Every screen,
  // hook and query key is identical in both modes, so what gets exercised
  // against fixtures is the same code that later talks to the API — a mock
  // layered above the client would prove nothing about the client.
  adapter: USE_MOCK
    ? async config => (await import("@/dev/mockRuntime")).mockAdapter(config)
    : undefined,
})

/**
 * The first request made without a token waits for the session to be resumed.
 * A hard reload renders and starts queries before the boot-time silent refresh
 * has answered; sent bare, they come back 401 and only recover through the
 * retry below. One shared check per page load: a visitor with no session is
 * asked once, not on every request (the refresh itself is single-flight in
 * @aoctech/auth-client, so the boot refresh and this one are one network call).
 */
let sessionCheck: Promise<unknown> | null = null

apiClient.interceptors.request.use(async config => {
  if (!accessToken && refresh) {
    sessionCheck ??= refresh().catch(() => null)
    await sessionCheck
  }
  if (accessToken) config.headers.Authorization = `Bearer ${accessToken}`
  return config
})

const MAINTENANCE_PATH = "/maintenance"
const LOGIN_PATH = "/login"

/**
 * How to get a fresh access token. Registered by AuthProvider rather than
 * imported, so this module stays the bottom of the dependency graph — an axios
 * client that imports React context is a client no test can construct.
 */
let refresh: (() => Promise<string | null>) | null = null
export const registerRefresh = (fn: () => Promise<string | null>) => {
  refresh = fn
}

/** Marks a request that has already been retried once, so a 401 answered by a
 *  refreshed token that is *also* rejected ends instead of looping. */
type Retriable = InternalAxiosRequestConfig & { _retried?: boolean }

/**
 * The two statuses that are about the session or the service rather than the
 * request.
 *
 * **401** is a token that expired mid-session, which for a thirty-minute PIX
 * window is routine rather than exceptional. One silent refresh and one retry;
 * the refresh itself is single-flight inside @aoctech/auth-client, so ten
 * queries failing at once produce one token request. If it cannot be refreshed
 * the session is genuinely over and the reader goes to /login.
 *
 * **503** is the service, not the request. Nothing on the current screen can
 * succeed and no per-block retry will help, so it goes to /maintenance, which
 * probes and brings the reader back — to the screen they were on, which is why
 * it is handed the path rather than left to guess. Guessing means "/", and "/"
 * is the marketing page: somebody interrupted mid-payment would come back to a
 * pitch for the product they were already paying for.
 *
 * Both still reject afterwards: a navigation is a full document load and the
 * in-flight promises have to settle rather than hang. `replace` and not
 * `assign` so the back button does not return to a screen that immediately
 * bounces here again.
 */
apiClient.interceptors.response.use(undefined, async (error: AxiosError) => {
  const status = error.response?.status
  const config = error.config as Retriable | undefined
  const onPage = (path: string) =>
    typeof window !== "undefined" && window.location.pathname === path

  if (status === 401 && config && !config._retried && refresh) {
    config._retried = true
    const token = await refresh()
    if (token) return apiClient(config)
    if (typeof window !== "undefined" && !onPage(LOGIN_PATH)) {
      window.location.replace(LOGIN_PATH)
    }
  }

  // A finance organization space answers 503 space-unavailable when ctech-account
  // cannot be reached, while the personal space keeps working. Redirecting the
  // whole app would take the working half down; the screen says so instead.
  if (status === 503 && !isSpaceUnavailable(error) && typeof window !== "undefined" && !onPage(MAINTENANCE_PATH)) {
    const from = window.location.pathname + window.location.search
    window.location.replace(`${MAINTENANCE_PATH}?from=${encodeURIComponent(from)}`)
  }

  return Promise.reject(error)
})

/**
 * The message to show a person, from whatever the failure actually was.
 *
 * A thrown request is a device that lost its connection, and saying so is
 * both truer and more useful than "erro interno". Everything the API refuses
 * carries a stable `code`, mapped to catalog text; a validation failure shows
 * its first field error, which is the actionable part. `title` and `detail`
 * are English fallbacks and are never shown.
 */
export function messageFor(error: unknown): string {
  const e = error as AxiosError<Problem>
  if (e?.code === "ERR_NETWORK" || e?.code === "ECONNABORTED") {
    return t("auth.errors.network")
  }
  const problem = e?.response?.data
  const first = problem?.errors?.[0]
  if (first?.code) return fieldErrorMessage(first)
  return codeMessage(problem?.code, e?.response?.status)
}

/** The field-level failures of a validation problem, already in reader text. */
export function fieldErrorsOf(error: unknown): { field: string; message: string }[] {
  const list = (error as AxiosError<Problem>)?.response?.data?.errors ?? []
  return list.map(err => ({field: err.field, message: fieldErrorMessage(err)}))
}

export function statusOf(error: unknown): number | undefined {
  return (error as AxiosError)?.response?.status
}

/** A signed-in person with no customer record in tenant zero. */
const NO_BILLING_ACCOUNT = "/problems/no-billing-account"

/**
 * Not every refusal is a failure.
 *
 * The portal answers 403 to somebody who has bought nothing yet, and it has to:
 * there is no customer behind the session, so no route below can serve them.
 * But that is a beginning, not a fault, and rendering it as one greets a new
 * reader with a red block quoting an internal sentence about "conta de
 * cobrança".
 *
 * Branching on `type` rather than on the status or the message: the other 403s
 * here (a closed account, a machine token on a person's route) are genuine
 * refusals that must keep looking like refusals, and `detail` is prose that
 * gets rewritten.
 */
export function isNoBillingAccount(error: unknown): boolean {
  return (error as AxiosError<Problem>)?.response?.data?.type === NO_BILLING_ACCOUNT
}

const SPACE_UNAVAILABLE = "/problems/space-unavailable"
const SPACE_NOT_FOUND = "/problems/space-not-found"

/** ctech-account could not verify the organization space; personal still works. */
export function isSpaceUnavailable(error: unknown): boolean {
  return (error as AxiosError<Problem>)?.response?.data?.type === SPACE_UNAVAILABLE
}

/** The selected organization space is not (or no longer) the reader's. */
/** The problem's stable code, for a screen that reacts to one refusal in particular. */
export function problemCode(error: unknown): string | undefined {
  return (error as AxiosError<Problem>)?.response?.data?.code
}

export function isSpaceNotFound(error: unknown): boolean {
  return (error as AxiosError<Problem>)?.response?.data?.type === SPACE_NOT_FOUND
}

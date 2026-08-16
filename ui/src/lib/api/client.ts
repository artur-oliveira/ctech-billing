"use client"

import axios, {type AxiosError, type InternalAxiosRequestConfig} from "axios"

import {USE_MOCK} from "@/lib/mockConfig"

/** RFC 7807, as ctech-go-common/problem emits it. */
export interface Problem {
  type: string
  title: string
  status: number
  detail?: string
  errors?: {field: string; message: string; tag?: string}[]
}

let accessToken: string | null = null
export const setAccessToken = (t: string | null) => {
  accessToken = t
}
export const getAccessToken = () => accessToken

export const apiClient = axios.create({
  baseURL: process.env.NEXT_PUBLIC_API_URL || "",
  timeout: 10_000,
  // Mock mode replaces the transport, not the calling code. Every screen,
  // hook and query key is identical in both modes, so what gets exercised
  // against fixtures is the same code that later talks to the API — a mock
  // layered above the client would prove nothing about the client.
  adapter: USE_MOCK
    ? async config => (await import("@/dev/mockRuntime")).mockAdapter(config)
    : undefined,
})

apiClient.interceptors.request.use(config => {
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
type Retriable = InternalAxiosRequestConfig & {_retried?: boolean}

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

  if (status === 503 && typeof window !== "undefined" && !onPage(MAINTENANCE_PATH)) {
    const from = window.location.pathname + window.location.search
    window.location.replace(`${MAINTENANCE_PATH}?from=${encodeURIComponent(from)}`)
  }

  return Promise.reject(error)
})

/**
 * The message to show a person, from whatever the failure actually was.
 *
 * A thrown request is a device that lost its connection, and saying so is
 * both truer and more useful than "erro interno". A 5xx is ours to apologise
 * for. Everything else already carries a `detail` written for the reader.
 */
export function messageFor(error: unknown): string {
  const e = error as AxiosError<Problem>
  if (e?.code === "ERR_NETWORK" || e?.code === "ECONNABORTED") {
    return "Não conseguimos falar com o servidor. Verifique sua conexão e tente de novo."
  }
  const problem = e?.response?.data
  if (problem?.detail) return problem.detail
  if (problem?.title) return problem.title
  return "Algo deu errado do nosso lado. Tente de novo em instantes."
}

export function statusOf(error: unknown): number | undefined {
  return (error as AxiosError)?.response?.status
}

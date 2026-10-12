"use client"

import {isAxiosError} from "axios"

import {apiClient} from "@/lib/api/client"
import type {FinanceCtx} from "@/lib/api/finance"
import {spaceHeader} from "@/lib/console/space"
import type {
  ClassifyBody, Connection, ConnectForm, ConnectionsScope, OnboardingStep, ProviderInfo, SyncResult, UnclassifiedLine,
} from "@/lib/api/connectionsTypes"

/**
 * Bank connections (BYOK). Every call names the organization and the mode,
 * added here and never by a caller; every write carries the caller's
 * Idempotency-Key (required by the signature). The credentials travel once, in
 * the connect form, and are never read back.
 */
const BASE = "/v1.0/console/connections"

function headers(s: ConnectionsScope, idempotencyKey?: string): Record<string, string> {
  const h: Record<string, string> = {"X-Billing-Space": `org:${s.org}`, "X-Billing-Mode": s.mode}
  if (idempotencyKey) h["Idempotency-Key"] = idempotencyKey
  return h
}

/** Organization and mode in every key: two of either are two caches. */
export const connectionKeys = {
  list: (s: ConnectionsScope) => ["connections", s.org, s.mode] as const,
  one: (s: ConnectionsScope, provider: string) => ["connections", s.org, s.mode, provider] as const,
  unclassified: (c: FinanceCtx, accountId: string) => ["finance", c.mode, spaceHeader(c.space), "unclassified", accountId] as const,
}

export async function listConnections(s: ConnectionsScope): Promise<{data: Connection[]; providers: ProviderInfo[]}> {
  const {data} = await apiClient.get<{data: Connection[]; providers: ProviderInfo[]}>(BASE, {headers: headers(s)})
  return data
}

/** A provider never connected (and never started) is null, not an error. */
export async function getConnection(s: ConnectionsScope, provider: string): Promise<Connection | null> {
  try {
    const {data} = await apiClient.get<Connection>(`${BASE}/${provider}`, {headers: headers(s)})
    return data
  } catch (e) {
    if (isNotFound(e)) return null
    throw e
  }
}

function isNotFound(e: unknown): boolean {
  if (isAxiosError(e)) return e.response?.status === 404
  const r = (e as {response?: {status?: number}} | null)?.response
  return r?.status === 404
}

/**
 * Live: the multipart form (the PEM files under `cert` and `key`). Test: an
 * empty object turns on CTech's own integration against the bank's sandbox.
 */
export async function connect(s: ConnectionsScope, provider: string, form: ConnectForm | null, key: string): Promise<Connection> {
  let body: FormData | Record<string, never> = {}
  if (form) {
    const fd = new FormData()
    fd.append("client_id", form.client_id)
    fd.append("client_secret", form.client_secret)
    fd.append("account", form.account)
    fd.append("pix_key", form.pix_key)
    fd.append("notify_email", form.notify_email)
    fd.append("backfill_months", String(form.backfill_months))
    fd.append("authorized", form.authorized ? "true" : "false")
    fd.append("cert", form.cert)
    fd.append("key", form.key)
    body = fd
  }
  const {data} = await apiClient.put<Connection>(`${BASE}/${provider}`, body, {headers: headers(s, key)})
  return data
}

export async function testConnection(s: ConnectionsScope, provider: string, key: string): Promise<Connection> {
  const {data} = await apiClient.post<Connection>(`${BASE}/${provider}/test`, {}, {headers: headers(s, key)})
  return data
}

export async function renewCertificate(s: ConnectionsScope, provider: string, cert: File, keyFile: File, key: string): Promise<Connection> {
  const fd = new FormData()
  fd.append("cert", cert)
  fd.append("key", keyFile)
  const {data} = await apiClient.post<Connection>(`${BASE}/${provider}/certificate`, fd, {headers: headers(s, key)})
  return data
}

export async function syncNow(s: ConnectionsScope, provider: string, key: string): Promise<SyncResult> {
  const {data} = await apiClient.post<SyncResult>(`${BASE}/${provider}/sync`, {}, {headers: headers(s, key)})
  return data
}

export async function setOnboarding(s: ConnectionsScope, provider: string, step: OnboardingStep, key: string): Promise<void> {
  await apiClient.put(`${BASE}/${provider}/onboarding`, {step}, {headers: headers(s, key)})
}

export async function disconnect(s: ConnectionsScope, provider: string, key: string): Promise<void> {
  await apiClient.delete(`${BASE}/${provider}`, {headers: headers(s, key)})
}

function financeHeaders(c: FinanceCtx, key?: string): Record<string, string> {
  const h: Record<string, string> = {"X-Billing-Mode": c.mode, "X-Billing-Space": spaceHeader(c.space)}
  if (key) h["Idempotency-Key"] = key
  return h
}

/** A synced bank account's lines still on "A classificar" (spec § 8.3). */
export async function listUnclassified(c: FinanceCtx, accountId: string): Promise<{data: UnclassifiedLine[]}> {
  const {data} = await apiClient.get<{data: UnclassifiedLine[]}>(`/v1.0/console/finance/accounts/${accountId}/unclassified`, {headers: financeHeaders(c)})
  return data
}

export async function classify(c: FinanceCtx, txId: string, body: ClassifyBody, key: string): Promise<{tx_id: string}> {
  const {data} = await apiClient.post<{tx_id: string}>(`/v1.0/console/finance/unclassified/${txId}/classify`, body, {headers: financeHeaders(c, key)})
  return data
}

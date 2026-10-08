"use client"

import {apiClient} from "@/lib/api/client"
import type {
  Account, Bill, BillPatch, CurrentSpace, Direction, FinanceSpaces, ListResponse, NewAccount, NewBill,
  NewRecurrence, Occurrence, PreviewInput, ProjectionMonth, Recurrence, RecurrencePatch, Settings, Settlement,
} from "@/lib/api/financeTypes"
import {type Space, spaceHeader} from "@/lib/console/space"
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
  const h: Record<string, string> = {"X-Billing-Mode": c.mode, "Billing-Space": spaceHeader(c.space)}
  if (idempotencyKey) h["Idempotency-Key"] = idempotencyKey
  return h
}

async function read<T>(c: FinanceCtx, url: string, params?: Record<string, unknown>): Promise<T> {
  const {data} = await apiClient.request<T>({method: "GET", url: BASE + url, headers: headers(c), params})
  return data
}

async function write<T>(c: FinanceCtx, method: "POST" | "PATCH" | "PUT", url: string, body: unknown, key: string): Promise<T> {
  const {data} = await apiClient.request<T>({method, url: BASE + url, headers: headers(c, key), data: body ?? {}})
  return data
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
  projection: (mode: Mode, space: Space, months: number) => ["finance", mode, spaceHeader(space), "projection", months] as const,
  accounts: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "accounts"] as const,
  settings: (mode: Mode, space: Space) => ["finance", mode, spaceHeader(space), "settings"] as const,
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

// --- recurrences ----------------------------------------------------------------

export const listRecurrences = (c: FinanceCtx) => read<ListResponse<Recurrence>>(c, "/recurrences")
export const createRecurrence = (c: FinanceCtx, body: NewRecurrence, idempotencyKey: string) =>
  write<Recurrence>(c, "POST", "/recurrences", body, idempotencyKey)
export const patchRecurrence = (c: FinanceCtx, id: string, body: RecurrencePatch, idempotencyKey: string) =>
  write<Recurrence>(c, "PATCH", `/recurrences/${encodeURIComponent(id)}`, body, idempotencyKey)
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

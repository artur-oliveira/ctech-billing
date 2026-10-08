import type {AxiosRequestConfig} from "axios"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {apiClient} from "@/lib/api/client"
import * as finance from "@/lib/api/finance"
import {financeKeys} from "@/lib/api/finance"
import type {Space} from "@/lib/console/space"

const personal: Space = {kind: "personal"}
const org: Space = {kind: "organization", organizationId: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}

let seen: AxiosRequestConfig[] = []
beforeEach(() => {
  seen = []
  vi.spyOn(apiClient, "request").mockImplementation(async (config: AxiosRequestConfig) => {
    seen.push(config)
    return {data: {data: [], spaces: []}, status: 200, statusText: "OK", headers: {}, config} as never
  })
})
afterEach(() => vi.restoreAllMocks())

const ctx = {mode: "test" as const, space: org}

// Review Focus 1: every call carries both headers, and writes carry the key.
describe("every finance call", () => {
  const reads: [string, () => Promise<unknown>][] = [
    ["listBills", () => finance.listBills(ctx, "payable")],
    ["getBill", () => finance.getBill(ctx, "b1")],
    ["listRecurrences", () => finance.listRecurrences(ctx)],
    ["previewRecurrence", () => finance.previewRecurrence(ctx, {expression: {kind: "day_of_month", day: 10}, start: "2026-01-01", count: 3})],
    ["getProjection", () => finance.getProjection(ctx, 6)],
    ["listAccounts", () => finance.listAccounts(ctx)],
    ["getSettings", () => finance.getSettings(ctx)],
    ["getCurrentSpace", () => finance.getCurrentSpace(ctx)],
  ]
  const writes: [string, () => Promise<unknown>][] = [
    ["createBill", () => finance.createBill(ctx, {direction: "payable", amount: 100, account_id: "a", category_id: "c", due_date: "2026-03-10"}, "K")],
    ["patchBill", () => finance.patchBill(ctx, "b1", {description: "x"}, "K")],
    ["settleBill", () => finance.settleBill(ctx, "b1", {}, "K")],
    ["cancelBill", () => finance.cancelBill(ctx, "b1", "K")],
    ["createRecurrence", () => finance.createRecurrence(ctx, {direction: "payable", amount: 1, category_id: "c", account_id: "a", expression: {kind: "day_of_month", day: 1}, start: "2026-01-01"}, "K")],
    ["patchRecurrence", () => finance.patchRecurrence(ctx, "r1", {amount: 2}, "K")],
    ["archiveRecurrence", () => finance.archiveRecurrence(ctx, "r1", "K")],
    ["createAccount", () => finance.createAccount(ctx, {name: "Banco", class: "asset"}, "K")],
    ["archiveAccount", () => finance.archiveAccount(ctx, "a1", "K")],
    ["setDefaultReceivingAccount", () => finance.setDefaultReceivingAccount(ctx, "a1", "K")],
  ]

  it.each([...reads, ...writes])("%s sends the mode and the space", async (_name, call) => {
    await call()
    const h = seen[0].headers as Record<string, string>
    expect(h["X-Billing-Mode"]).toBe("test")
    expect(h["Billing-Space"]).toBe("org:0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
  })

  it.each(writes)("%s sends the Idempotency-Key", async (_name, call) => {
    await call()
    expect((seen[0].headers as Record<string, string>)["Idempotency-Key"]).toBe("K")
  })

  it.each(reads)("%s sends no Idempotency-Key", async (_name, call) => {
    await call()
    expect((seen[0].headers as Record<string, string>)["Idempotency-Key"]).toBeUndefined()
  })

  it("lists spaces without a space header (there is none yet)", async () => {
    await finance.getFinanceSpaces()
    expect((seen[0].headers as Record<string, string> | undefined)?.["Billing-Space"]).toBeUndefined()
  })
})

describe("financeKeys", () => {
  it("carry the mode and the space, so two spaces or modes never share a cache entry", () => {
    const keys = [
      financeKeys.bills("live", personal, "payable"),
      financeKeys.bills("live", org, "payable"),
      financeKeys.bills("test", personal, "payable"),
    ]
    expect(keys[0].slice(0, 3)).toEqual(["finance", "live", "personal"])
    expect(new Set(keys.map(k => JSON.stringify(k))).size).toBe(3)
    for (const make of [financeKeys.accounts, financeKeys.recurrences, financeKeys.settings, financeKeys.space]) {
      expect(JSON.stringify(make("live", personal))).not.toBe(JSON.stringify(make("live", org)))
      expect(JSON.stringify(make("live", personal))).not.toBe(JSON.stringify(make("test", personal)))
    }
  })
})

// A write without a key is a compile error, not a runtime surprise.
// @ts-expect-error — createBill requires an idempotency key
void (() => finance.createBill(ctx, {direction: "payable", amount: 1, account_id: "a", category_id: "c", due_date: "2026-03-10"}))

describe("a space that is no longer the reader's", () => {
  it("falls back to personal when any finance call answers 404 space-not-found", async () => {
    const {getSpace, setSpace} = await import("@/lib/console/space")
    setSpace(org)
    vi.mocked(apiClient.request).mockRejectedValueOnce({response: {status: 404, data: {type: "/problems/space-not-found"}}})
    await expect(finance.listAccounts(ctx)).rejects.toBeTruthy()
    expect(getSpace()).toEqual({kind: "personal"})
    window.localStorage.clear()
  })
})

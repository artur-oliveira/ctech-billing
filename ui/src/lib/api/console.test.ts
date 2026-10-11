import {afterEach, describe, expect, it, vi} from "vitest"

import {apiClient} from "@/lib/api/client"
import {
  consoleKeys, getConsoleInvoicePDF, getConsoleSession, getConsoleSettings, listConsoleInvoices, revealTaxID, voidInvoice,
} from "@/lib/api/console"
import type {ConsoleScope} from "@/lib/console/merchant"

const SCOPE: ConsoleScope = {mode: "test", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"}

afterEach(() => vi.restoreAllMocks())

describe("console API", () => {
  it("sends the mode and the organization on every billing-half call", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {}})
    const post = vi.spyOn(apiClient, "post").mockResolvedValue({data: {tax_id: "1"}})
    await getConsoleSettings(SCOPE)
    await listConsoleInvoices(SCOPE, 2026, 10)
    await getConsoleInvoicePDF(SCOPE, "in_1", "pt-BR")
    await voidInvoice(SCOPE, "in_1")
    await revealTaxID(SCOPE, "cus_1")
    const configs = [...get.mock.calls, ...post.mock.calls].map(c => c[c.length - 1] as {headers: Record<string, string>})
    expect(configs).toHaveLength(5)
    for (const config of configs) {
      expect(config.headers).toEqual(expect.objectContaining({"X-Billing-Mode": "test", "X-Billing-Space": `org:${SCOPE.org}`}))
    }
  })

  it("asks for the session with no mode and no space", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {organizations: [], organizations_unavailable: false}})
    await getConsoleSession()
    expect(get).toHaveBeenCalledWith("/v1.0/console/session")
  })

  it("keys every query by organization and mode", () => {
    expect(consoleKeys.session()).toEqual(["console", "session"])
    expect(consoleKeys.invoices(SCOPE)).toEqual(["console", SCOPE.org, "test", "invoices"])
    expect(consoleKeys.invoice(SCOPE, "in_1")).toEqual(["console", SCOPE.org, "test", "invoices", "detail", "in_1"])
    expect(consoleKeys.invoices({...SCOPE, org: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"})).not.toEqual(consoleKeys.invoices(SCOPE))
  })
})

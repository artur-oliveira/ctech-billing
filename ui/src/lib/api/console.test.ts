import {afterEach, describe, expect, it, vi} from "vitest"

import {apiClient} from "@/lib/api/client"
import {
  applyForMerchant, approveApplication, consoleKeys, getConsoleInvoicePDF, getConsoleSession, getConsoleSettings,
  getMerchantApplication, listConsoleInvoices, listMerchantCompanies, rejectApplication, revealTaxID, voidInvoice,
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

describe("merchant applications", () => {
  it("send the organization, and the application keys carry no mode", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {data: []}})
    const post = vi.spyOn(apiClient, "post").mockResolvedValue({data: {id: "apl_1"}})
    await listMerchantCompanies(SCOPE)
    await applyForMerchant(SCOPE, {company_id: "cmp_1", description: "x".repeat(20), contact_email: "c@x.example"})
    await approveApplication(SCOPE, "apl_1")
    await rejectApplication(SCOPE, "apl_1", "motivo bem claro")
    for (const c of [...get.mock.calls, ...post.mock.calls]) {
      expect((c[c.length - 1] as {headers: Record<string, string>}).headers).toEqual(expect.objectContaining({"X-Billing-Space": `org:${SCOPE.org}`}))
    }
    expect(post).toHaveBeenCalledWith("/v1.0/console/applications/apl_1/reject", {reason: "motivo bem claro"}, expect.anything())
    expect(consoleKeys.merchantApplication(SCOPE)).toEqual(["console", SCOPE.org, "merchant", "application"])
    expect(consoleKeys.merchantApplication({...SCOPE, mode: "live"})).toEqual(consoleKeys.merchantApplication(SCOPE))
    expect(consoleKeys.applications(SCOPE, "pending")).toEqual(["console", SCOPE.org, "test", "applications", "pending"])
  })

  it("reads 'never applied' as null, not as a failure", async () => {
    vi.spyOn(apiClient, "get").mockRejectedValue({response: {status: 404, data: {code: "no_application"}}})
    await expect(getMerchantApplication(SCOPE)).resolves.toBeNull()
  })

  it("passes on that ctech-account cut the company list", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({data: {data: [{company_id: "cmp_1", legal_name: "A", tax_id: "1"}], truncated: true}})
    await expect(listMerchantCompanies(SCOPE)).resolves.toEqual({companies: [{company_id: "cmp_1", legal_name: "A", tax_id: "1"}], truncated: true})
  })
})

import {beforeEach, describe, expect, it} from "vitest"

import type {Invoice, ListResponse, PaymentResult} from "@/lib/api/types"
import {mockAdapter, setScenario, settleInvoice} from "./mockRuntime"

// The mock is the only way most of these states are ever seen, so it gets the
// same scrutiny as the code it stands in for. A fixture that quietly serves the
// wrong scenario means a screen designed against a state that does not exist.

type Req = Parameters<typeof mockAdapter>[0]
const call = <T>(url: string, method: "get" | "post") =>
  mockAdapter({url, method, headers: {}} as unknown as Req) as Promise<{ data: T }>
const get = <T>(url: string) => call<T>(url, "get")
const post = <T>(url: string) => call<T>(url, "post")

beforeEach(() => {
  window.localStorage.clear()
  window.history.replaceState({}, "", "/")
})

describe("scenario selection", () => {
  it("serves the fixture the switcher chose", async () => {
    setScenario("vencida")
    const {data} = await get<ListResponse<Invoice>>("/v1/portal/invoices")
    expect(data.data[0].tone).toBe("urgent")
    expect(data.data[0].state).toContain("Vencida")
  })

  it("promotes ?scenario= to storage so client navigation does not lose it", async () => {
    window.history.replaceState({}, "", "/?scenario=paga")
    await get<ListResponse<Invoice>>("/v1/portal/invoices")

    window.history.replaceState({}, "", "/invoices")
    const {data} = await get<ListResponse<Invoice>>("/v1/portal/invoices")
    expect(data.data[0].state).toBe("Paga")
  })
})

describe("paying", () => {
  it("returns a PIX charge that has not already expired", async () => {
    setScenario("vence_em_3_dias")
    const {data} = await post<PaymentResult>("/v1/portal/invoices/inv_mock_0042/pay")

    expect(data.payment.pix_code.length).toBeGreaterThan(80)
    expect(new Date(data.payment.expires_at).getTime()).toBeGreaterThan(Date.now())
  })

  it("refuses an invoice the server would refuse", async () => {
    setScenario("paga")
    await expect(post("/v1/portal/invoices/inv_mock_0042/pay")).rejects.toMatchObject({
      response: {status: 422},
    })
  })

  it("shows the invoice as settled once the stream reports it", async () => {
    setScenario("vence_em_3_dias")
    settleInvoice("inv_mock_0042")

    const {data} = await get<Invoice>("/v1/portal/invoices/inv_mock_0042")
    expect(data.state).toBe("Paga")
    expect(data.amount_due).toBe(0)
    expect(data.payable).toBe(false)
  })
})

describe("failure fixtures", () => {
  it("fails like a lost connection, not like a server error", async () => {
    setScenario("erro_de_rede")
    await expect(get("/v1/portal/invoices")).rejects.toMatchObject({code: "ERR_NETWORK"})
  })

  it("404s a route it has no fixture for, instead of answering with nothing", async () => {
    setScenario("em_dia")
    await expect(get("/v1/portal/nao_existe")).rejects.toMatchObject({
      response: {status: 404},
    })
  })
})

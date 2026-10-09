import {beforeEach, describe, expect, it} from "vitest"

import {FINANCE_MOCK_ORG, financeMock, resetFinanceMock} from "@/dev/financeMockData"

type Req = {method?: string; url: string; headers?: Record<string, string>; data?: unknown; params?: Record<string, unknown>}

const personal = {"X-Billing-Mode": "live", "X-Billing-Space": "personal"}
const org = {"X-Billing-Mode": "live", "X-Billing-Space": `org:${FINANCE_MOCK_ORG}`}
const call = (r: Req) => financeMock({method: "get", ...r, url: "/v1.0/console/finance" + r.url})

beforeEach(() => resetFinanceMock())

describe("the finance mock enforces the contract", () => {
  it("refuses a request without the mode or the space", () => {
    expect(call({url: "/accounts", headers: {"X-Billing-Mode": "live"}}).status).toBe(400)
    expect(call({url: "/accounts", headers: {"X-Billing-Space": "personal"}}).status).toBe(400)
  })

  it("lists spaces without a space header", () => {
    const r = call({url: "/spaces", headers: {}})
    expect(r.status).toBe(200)
    expect((r.data as {spaces: {kind: string}[]}).spaces[0].kind).toBe("personal")
  })

  it("refuses a write without an Idempotency-Key and replays a repeated one", () => {
    const body = {direction: "payable", amount: 5000, account_id: "conta-corrente", category_id: "aluguel", due_date: "2026-03-20"}
    expect(call({method: "post", url: "/bills", headers: personal, data: body}).status).toBe(400)
    const a = call({method: "post", url: "/bills", headers: {...personal, "Idempotency-Key": "k1"}, data: body})
    const b = call({method: "post", url: "/bills", headers: {...personal, "Idempotency-Key": "k1"}, data: body})
    expect(a.status).toBe(201)
    expect((b.data as {id: string}).id).toBe((a.data as {id: string}).id)
    const list = call({url: "/bills", headers: personal, params: {direction: "payable"}}).data as {data: {id: string}[]}
    expect(list.data.filter(x => x.id === (a.data as {id: string}).id)).toHaveLength(1)
  })

  it("answers 404 space-not-found for an organization that is not the reader's", () => {
    const r = call({url: "/accounts", headers: {...org, "X-Billing-Space": "org:0190a1b2-c3d4-7e5f-8a9b-ffffffffffff"}})
    expect(r.status).toBe(404)
    expect((r.data as {type: string}).type).toBe("/problems/space-not-found")
  })

  it("keeps state: settle removes a bill from the open list and moves the balance; a second settle is a 409", () => {
    const open = () => (call({url: "/bills", headers: personal, params: {direction: "payable"}}).data as {data: {id: string; amount: number}[]}).data
    const bill = open()[0]
    const balance = () => (call({url: "/accounts", headers: personal}).data as {data: {id: string; balance: number}[]}).data.find(a => a.id === "conta-corrente")!.balance
    const before = balance()
    const settle = (k: string) => call({method: "post", url: `/bills/${bill.id}/settle`, headers: {...personal, "Idempotency-Key": k}, data: {}})
    expect(settle("s1").status).toBe(200)
    expect(open().find(b => b.id === bill.id)).toBeUndefined()
    expect(balance()).toBe(before - bill.amount)
    const again = settle("s2")
    expect(again.status).toBe(409)
    expect((again.data as {type: string}).type).toBe("/problems/invalid-transition")
  })

  it("holds different data per space", () => {
    const names = (h: Record<string, string>) => (call({url: "/accounts", headers: h}).data as {data: {name: string}[]}).data.map(a => a.name).join()
    expect(names(personal)).not.toBe(names(org))
  })

  it("enforces the role's verbs (the organization fixture is a member: no configure)", () => {
    const r = call({method: "post", url: "/accounts", headers: {...org, "Idempotency-Key": "a1"}, data: {name: "X", class: "asset"}})
    expect(r.status).toBe(403)
  })

  it("keeps virtual occurrences apart in the projection", () => {
    const months = (call({url: "/projection", headers: personal, params: {months: 6}}).data as {data: {virtual: number; payable: number}[]}).data
    expect(months).toHaveLength(6)
    expect(months.some(m => m.virtual !== 0)).toBe(true)
  })

  it("previews occurrences or refuses a bad count", () => {
    const ok = call({method: "post", url: "/recurrences/preview", headers: personal, data: {expression: {kind: "day_of_month", day: 31}, start: "2026-01-01", count: 3, from: "2026-01-01"}})
    expect((ok.data as {data: {nominal: string}[]}).data.map(o => o.nominal)).toEqual(["2026-01-31", "2026-02-28", "2026-03-31"])
    expect(call({method: "post", url: "/recurrences/preview", headers: personal, data: {expression: {kind: "day_of_month", day: 10}, start: "2026-01-01", count: 0}}).status).toBe(422)
  })
})

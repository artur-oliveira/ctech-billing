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

describe("the finance mock's ledger facts", () => {
  const w = (url: string, data: unknown, key: string) =>
    call({method: "post", url, headers: {...personal, "Idempotency-Key": key}, data})
  const month = () => new Date().toISOString().slice(0, 7)
  const day = () => `${month()}-01`
  const balance = (id: string) =>
    (call({url: "/accounts", headers: personal}).data as {data: {id: string; balance: number}[]}).data.find(a => a.id === id)!.balance

  it("moves both balances on a transfer and keeps it out of the cash flow", () => {
    const before = [balance("conta-corrente"), balance("poupanca")]
    expect(w("/transfers", {from_account_id: "conta-corrente", to_account_id: "poupanca", amount: 1000, date: day()}, "t1").status).toBe(201)
    expect([balance("conta-corrente"), balance("poupanca")]).toEqual([before[0] - 1000, before[1] + 1000])
    const cf = call({url: "/reports/cash-flow", headers: personal, params: {from: month(), to: month()}})
    const m = (cf.data as {months: {in: number; out: number}[]}).months[0]
    expect([m.in, m.out]).toEqual([0, 0])
  })

  it("puts an unsettled bill back in the open list", () => {
    expect(w("/bills/b-mercado/settle", {}, "s1").status).toBe(200)
    expect(w("/bills/b-mercado/unsettle", {}, "u1").status).toBe(200)
    const open = call({url: "/bills", headers: personal, params: {direction: "payable"}}).data as {data: {id: string; auto_settle: boolean}[]}
    expect(open.data.find(b => b.id === "b-mercado")?.auto_settle).toBe(false)
  })

  it("refuses a second opening balance on one account", () => {
    expect(w("/accounts/poupanca/opening-balance", {amount: 500, date: day()}, "o1").status).toBe(201)
    expect(w("/accounts/poupanca/opening-balance", {amount: 500, date: day()}, "o2").status).toBe(409)
  })
})

describe("the finance mock's cards", () => {
  const w = (url: string, data: unknown, key: string) =>
    call({method: "post", url, headers: {...personal, "Idempotency-Key": key}, data})
  const r = (url: string) => call({url, headers: personal}).data as never
  const today = new Date()
  const ym = (offset: number) => {
    const d = new Date(today.getFullYear(), today.getMonth() + offset, 1)
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`
  }
  const card = () => (w("/cards", {name: "Visa", closing_day: 28, due_day: 5, paying_account_id: "conta-corrente"}, "c").data as {id: string}).id

  it("bills a purchase in installments on the next statements and closes into a bill", () => {
    const id = card()
    const d = `${ym(0)}-01`
    expect(w(`/cards/${id}/purchases`, {date: d, description: "TV", category_id: "mercado", total: 30000, installments: 3}, "p").status).toBe(201)
    for (const i of [0, 1, 2]) expect((r(`/cards/${id}/statements/${ym(i)}`) as {total: number}).total).toBe(10000)
    const closed = w(`/cards/${id}/close`, {month: ym(0)}, "x").data as {status: string; bill_id: string}
    expect(closed.status).toBe("closed")
    const open = call({url: "/bills", headers: personal, params: {direction: "payable"}}).data as {data: {id: string}[]}
    expect(open.data.some(b => b.id === closed.bill_id)).toBe(true)
  })

  it("refund credits what was billed, advance moves the rest to the open statement", () => {
    const id = card()
    const p = (w(`/cards/${id}/purchases`, {date: `${ym(0)}-01`, description: "Sofá", category_id: "mercado", total: 30000, installments: 3}, "p").data as {id: string}).id
    w(`/cards/${id}/close`, {month: ym(0)}, "x")
    expect(w(`/cards/${id}/purchases/${p}/advance`, {}, "a").status).toBe(200)
    expect((r(`/cards/${id}/statements/${ym(1)}`) as {total: number}).total).toBe(20000)
    expect(w(`/cards/${id}/purchases/${p}/refund`, {}, "r").status).toBe(200)
    expect((r(`/cards/${id}/statements/${ym(1)}`) as {total: number}).total).toBe(-10000)
  })
})

describe("the mock imports statements (F6)", () => {
  const w = (url: string, data: unknown, key: string) =>
    call({method: "post", url, headers: {...personal, "Idempotency-Key": key}, data})
  const r = (url: string) => call({url, headers: personal}).data as never
  // A synthetic OFX: invented values only.
  const ofx = (amount: string, date: string) => btoa(`OFXHEADER:100\r\n<OFX><STMTRS><CURDEF>BRL<BANKTRANLIST>
<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>${date.replaceAll("-", "")}<TRNAMT>${amount}<FITID>X1<MEMO>Compra teste</STMTTRN>
</BANKTRANLIST></STMTRS></OFX>`)

  it("adds a file once: the same file again answers 200 with nothing new", () => {
    const today = new Date().toISOString().slice(0, 10)
    const body = {account_id: "conta-corrente", format: "ofx", content: ofx("-12.34", today)}
    const first = w("/imports", body, "u1")
    expect(first.status).toBe(201)
    expect((first.data as {lines: number}).lines).toBe(1)
    const second = w("/imports", body, "u2")
    expect(second.status).toBe(200)
    expect(second.data).toMatchObject({lines: 0, duplicates: 1})
    expect((second.data as {id?: string}).id).toBeUndefined()
  })

  it("offers the forecast bill and settles it on match", () => {
    const bills = (call({url: "/bills", headers: personal, params: {direction: "payable"}}).data as {data: {id: string; amount: number; due_date: string}[]}).data
    const target = bills.find(b => b.id === "b-mercado")!
    const up = w("/imports", {account_id: "conta-corrente", format: "ofx", content: ofx(`-${(target.amount / 100).toFixed(2)}`, target.due_date)}, "u3")
    const id = (up.data as {id: string}).id
    const detail = r(`/imports/${id}`) as {lines: {n: number; candidates: {id: string}[]}[]}
    expect(detail.lines[0].candidates.map(c => c.id)).toContain("b-mercado")
    expect(w(`/imports/${id}/lines/1/match`, {bill_id: "b-mercado"}, "m1").status).toBe(200)
    expect(w(`/imports/${id}/lines/1/ignore`, {}, "i1").status).toBe(409)
    expect((r(`/imports/${id}`) as {import: {pending: number}}).import.pending).toBe(0)
  })

  it("ignores and reopens a line, and creates a paid bill from one", () => {
    expect(w("/imports/imp-seed/lines/2/ignore", {}, "i2").status).toBe(200)
    expect(w("/imports/imp-seed/lines/2/reopen", {}, "o2").status).toBe(200)
    const made = w("/imports/imp-seed/lines/2/new", {category_id: "mercado"}, "n2")
    expect(made.status).toBe(201)
    expect((made.data as {bill: {status: string; origin: string}}).bill).toMatchObject({status: "paid", origin: "import"})
  })

  it("asks for the CSV columns before reading a CSV", () => {
    const csv = btoa("05/03/2026;Café;-5,00\n")
    expect(w("/imports", {account_id: "conta-corrente", format: "csv", content: csv}, "c1").data).toMatchObject({code: "csv_mapping_required"})
    const m = {delimiter: ";", decimal: ",", date_format: "dd/mm/yyyy", skip_rows: 0, date_column: 1, description_column: 2, amount_column: 3, debit_column: 0}
    expect(call({method: "put", url: "/accounts/conta-corrente/csv-mapping", headers: {...personal, "Idempotency-Key": "m"}, data: m}).status).toBe(200)
    const up = w("/imports", {account_id: "conta-corrente", format: "csv", content: csv}, "c2")
    expect(up.status).toBe(201)
    expect((up.data as {lines: number}).lines).toBe(1)
  })
})

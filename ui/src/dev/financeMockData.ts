/**
 * A small stateful finance backend for `npm run dev:mock`.
 *
 * It is strict about the contract on purpose: no `X-Billing-Mode` or
 * `X-Billing-Space`, 400; a write without `Idempotency-Key`, 400; a repeated key
 * replays the stored response; an organization that is not the reader's, 404
 * space-not-found; a verb the role does not hold, 403. A mock that let the UI
 * forget a header would make a broken screen look fine — the opposite of why
 * the mock sits below the client (lib/api/client.ts).
 *
 * State lives per (mode, space), so switching either shows different data, as
 * it does against the real API. Recurrence previews are computed for
 * `day_of_month`, `weekly` and `yearly`; the business-day kinds are approximated
 * (this is a development fixture, not the scheduler).
 */
import type {
  Account, AccountClass, Bill, CashFlowMonth, DREGroupLine, Direction, ExpressionJSON, FinanceSpaceEntry, ProjectionMonth,
  Recurrence, StatementEntry, TxKind, Verb,
} from "@/lib/api/financeTypes"

export const FINANCE_MOCK_ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

type Req = {method?: string; url: string; headers?: Record<string, unknown>; data?: unknown; params?: Record<string, unknown>}
type Res = {status: number; data: unknown}

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"]
const MEMBER: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import"]

const SPACES: FinanceSpaceEntry[] = [
  {kind: "personal", label: "Pessoal", verbs: ALL},
  {kind: "organization", organization_id: FINANCE_MOCK_ORG, label: "Acme Serviços LTDA", role: "member", verbs: MEMBER},
]

/** One leg on a cash account, as the API's entry rows (flow "-" = not cash flow). */
interface MEntry {
  account: string
  tx: string
  date: string
  amount: number
  kind: TxKind
  flow: string
  memo: string
  ref?: string
  reversal?: boolean
}

interface SpaceState {
  accounts: Account[]
  bills: Bill[]
  recurrences: Recurrence[]
  defaultReceiving?: string
  seq: number
  /** Cash entries posted in this session; seeded balances predate them. */
  entries: MEntry[]
  reversed: Set<string>
  openings: Set<string>
}

// ---- dates (civil, no Date parsing of ISO strings) ---------------------------

function iso(y: number, m: number, d: number): string {
  return `${y}-${String(m).padStart(2, "0")}-${String(d).padStart(2, "0")}`
}
function parts(s: string): [number, number, number] {
  const [y, m, d] = s.split("-").map(Number)
  return [y, m, d]
}
function daysIn(y: number, m: number) {
  return new Date(y, m, 0).getDate()
}
function addDays(s: string, n: number): string {
  const [y, m, d] = parts(s)
  const t = new Date(y, m - 1, d + n, 12)
  return iso(t.getFullYear(), t.getMonth() + 1, t.getDate())
}
function todayIso(): string {
  const t = new Date()
  return iso(t.getFullYear(), t.getMonth() + 1, t.getDate())
}
function monthOffset(s: string, n: number): [number, number] {
  const [y, m] = parts(s)
  const t = new Date(y, m - 1 + n, 1)
  return [t.getFullYear(), t.getMonth() + 1]
}

// ---- fixtures ------------------------------------------------------------------

function seed(kind: "personal" | "org", mode: string): SpaceState {
  const today = todayIso()
  const live = mode === "live"
  const acct = (id: string, name: string, cls: AccountClass, balance = 0, extra: Partial<Account> = {}): Account =>
    ({id, name, class: cls, system: false, archived: false, balance, ...extra})
  const bill = (id: string, dir: Direction, amount: number, due: string, category: string, description: string, extra: Partial<Bill> = {}): Bill => ({
    id, direction: dir, amount, account_id: "conta-corrente", category_id: category, description,
    competence_date: due, due_date: due, status: "forecast", origin: "manual", auto_settle: false, ...extra,
  })
  const ledger = {entries: [] as MEntry[], reversed: new Set<string>(), openings: new Set<string>()}
  if (kind === "personal") {
    return {
      ...ledger,
      seq: 1,
      accounts: [
        acct("conta-corrente", "Conta corrente", "asset", live ? 842_315 : 50_000),
        acct("poupanca", "Poupança", "asset", live ? 1_250_000 : 0),
        acct("salario", "Salário", "income", 0, {dre_group: "gross_revenue"}),
        acct("aluguel", "Aluguel", "expense", 0, {dre_group: "operating_expenses"}),
        acct("mercado", "Mercado", "expense", 0, {dre_group: "operating_expenses"}),
        acct("juros", "Juros e multas", "expense", 0, {dre_group: "financial_result"}),
        acct("assinaturas-antigas", "Assinaturas antigas", "expense", 0, {dre_group: "operating_expenses", archived: true}),
      ],
      bills: [
        bill("b-aluguel", "payable", 180_000, addDays(today, -3), "aluguel", "Aluguel do apartamento"),
        bill("b-mercado", "payable", 42_590, today, "mercado", "Compra do mês"),
        bill("b-internet", "payable", 11_990, addDays(today, 6), "aluguel", "Internet", {auto_settle: true}),
        bill("b-freela", "receivable", 350_000, addDays(today, 9), "salario", "Projeto freelance"),
      ],
      recurrences: [{
        id: "r-aluguel", direction: "payable", amount: 180_000, category_id: "aluguel", account_id: "conta-corrente",
        description: "Aluguel do apartamento", expression: {kind: "day_of_month", day: 10}, start: "2026-01-01",
        business_day_adjust: "roll_forward", auto_settle: false, archived: false,
      }],
    }
  }
  return {
    ...ledger,
    seq: 1,
    accounts: [
      acct("conta-corrente", "Conta PJ", "asset", live ? 4_120_000 : 0),
      acct("vendas", "Vendas", "income", 0, {dre_group: "gross_revenue"}),
      acct("fornecedores", "Fornecedores", "expense", 0, {dre_group: "costs"}),
    ],
    bills: [bill("b-fornecedor", "payable", 980_000, addDays(today, 2), "fornecedores", "Fornecedor de insumos")],
    recurrences: [],
  }
}

const state = new Map<string, SpaceState>()
const replays = new Map<string, Res>()

export function resetFinanceMock() {
  state.clear()
  replays.clear()
}

// ---- helpers -------------------------------------------------------------------

const problem = (status: number, type: string, title: string, detail: string): Res =>
  ({status, data: {type, title, status, detail}})
const ok = (data: unknown, status = 200): Res => ({status, data})

function header(r: Req, name: string): string | undefined {
  const v = r.headers?.[name]
  return typeof v === "string" && v !== "" ? v : undefined
}

function body<T>(r: Req): T {
  return (typeof r.data === "string" ? JSON.parse(r.data) : r.data ?? {}) as T
}

// ---- occurrences (mock-grade) ----------------------------------------------------

function occurrences(expr: ExpressionJSON, from: string, count: number): {nominal: string; due: string}[] {
  const out: {nominal: string; due: string}[] = []
  const roll = (s: string) => {
    let d = s
    while ([0, 6].includes(new Date(...(([y, m, dd]) => [y, m - 1, dd, 12] as const)(parts(d))).getDay())) d = addDays(d, 1)
    return d
  }
  const base = expr.kind === "difference" ? expr.include : expr
  for (let i = 0; out.length < count && i < 400; i++) {
    let nominal: string | null = null
    if (base.kind === "weekly") {
      nominal = addDays(base.anchor, 7 * base.every * i)
    } else if (base.kind === "yearly") {
      const [y] = parts(from)
      nominal = iso(y + i, base.month, Math.min(base.day, daysIn(y + i, base.month)))
    } else {
      const [y, m] = monthOffset(from, i)
      const day = base.kind === "day_of_month" ? base.day : 5
      nominal = iso(y, m, Math.min(day, daysIn(y, m)))
    }
    if (nominal < from) continue
    if (expr.kind === "difference" && expr.exclude.kind === "months_of_year" && expr.exclude.months.includes(parts(nominal)[1])) continue
    out.push({nominal, due: roll(nominal)})
  }
  return out
}

// ---- the handler -----------------------------------------------------------------

export function financeMock(r: Req): Res {
  const method = (r.method ?? "get").toLowerCase()
  const path = r.url.replace(/^.*\/v1\.0\/console\/finance/, "").split("?")[0]

  if (path === "/spaces" && method === "get") return ok({spaces: SPACES, organizations_unavailable: false})

  const mode = header(r, "X-Billing-Mode")
  const selector = header(r, "X-Billing-Space")
  if (!mode || !selector) return problem(400, "about:blank", "Bad Request", "informe X-Billing-Mode e X-Billing-Space")
  const entry = selector === "personal"
    ? SPACES[0]
    : SPACES.find(s => `org:${s.organization_id}` === selector)
  if (!entry) return problem(404, "/problems/space-not-found", "Space not found", "espaço não encontrado")
  const can = (v: Verb) => entry.verbs.includes(v)

  const key = `${mode}|${selector}`
  if (!state.has(key)) state.set(key, seed(entry.kind === "personal" ? "personal" : "org", mode))
  const s = state.get(key)!

  const isWrite = method !== "get" && path !== "/recurrences/preview"
  if (isWrite) {
    const idem = header(r, "Idempotency-Key")
    if (!idem) return problem(400, "about:blank", "Bad Request", "cabeçalho Idempotency-Key obrigatório")
    const replayKey = `${key}|${method}|${path}|${idem}`
    const stored = replays.get(replayKey)
    if (stored) return stored
    const res = route(method, path, r, s, can)
    if (res.status >= 200 && res.status < 300) replays.set(replayKey, res)
    return res
  }
  return route(method, path, r, s, can)
}

function route(method: string, path: string, r: Req, s: SpaceState, can: (v: Verb) => boolean): Res {
  const forbidden = () => problem(403, "about:blank", "Forbidden", "seu papel não permite esta operação")
  const today = todayIso()
  const withBucket = (b: Bill): Bill =>
    b.status === "forecast" ? {...b, bucket: b.due_date < today ? "overdue" : b.due_date === today ? "today" : "upcoming"} : b
  const nextId = (p: string) => `${p}-${++s.seq}`
  const billMatch = path.match(/^\/bills\/([^/]+)(?:\/(settle|cancel|unsettle))?$/)
  const post = (entries: Omit<MEntry, "tx">[]) => {
    const tx = nextId("tx")
    for (const e of entries) {
      s.entries.push({...e, tx})
      const a = s.accounts.find(x => x.id === e.account)
      if (a) a.balance += e.amount
    }
    return tx
  }
  const reverse = (tx: string) => {
    s.reversed.add(tx)
    return post(s.entries.filter(e => e.tx === tx).map(e => ({...e, amount: -e.amount, reversal: true, date: today})))
  }
  const recMatch = path.match(/^\/recurrences\/([^/]+)(?:\/archive)?$/)

  if (path === "/space") return ok({kind: "personal", mode: "live", verbs: ALL})

  // bills
  if (path === "/bills" && method === "get") {
    const dir = r.params?.direction as Direction
    const data = s.bills.filter(b => b.direction === dir && b.status === "forecast")
      .sort((a, b) => a.due_date.localeCompare(b.due_date)).map(withBucket)
    return ok({data, has_more: false})
  }
  if (path === "/bills" && method === "post") {
    if (!can("finance.write")) return forbidden()
    const b = body<Partial<Bill>>(r)
    if (b.auto_settle && !can("finance.settle")) return forbidden()
    if (!b.amount || b.amount <= 0) return problem(422, "about:blank", "Unprocessable", "amount must be positive")
    const bill: Bill = {
      id: nextId("b"), direction: b.direction!, amount: b.amount, account_id: b.account_id!, category_id: b.category_id!,
      description: b.description, competence_date: b.competence_date ?? b.due_date!, due_date: b.due_date!,
      status: "forecast", origin: "manual", auto_settle: !!b.auto_settle,
    }
    s.bills.push(bill)
    return ok(withBucket(bill), 201)
  }
  if (billMatch) {
    const bill = s.bills.find(b => b.id === billMatch[1])
    if (!bill) return problem(404, "about:blank", "Not Found", "recurso não encontrado")
    const action = billMatch[2]
    if (method === "get" && !action) return ok(withBucket(bill))
    const transition = () => problem(409, "/problems/invalid-transition", "Invalid Transition", "esta conta mudou enquanto você a via")
    if (action === "settle") {
      if (!can("finance.settle")) return forbidden()
      if (bill.status !== "forecast") return transition()
      const req = body<{paid_amount?: number; paid_date?: string; difference_category_id?: string}>(r)
      const paid = req.paid_amount ?? bill.amount
      if (paid !== bill.amount && !req.difference_category_id) return problem(422, "about:blank", "Unprocessable", "a different amount needs a category for the gap")
      const date = req.paid_date ?? today
      post([{
        account: bill.account_id, date, amount: bill.direction === "payable" ? -paid : paid, kind: "settlement",
        flow: bill.category_id, memo: bill.description ?? "Sem descrição", ref: `bill:${bill.id}`,
      }])
      Object.assign(bill, {status: "paid", paid_date: date})
      return ok(bill)
    }
    if (action === "unsettle") {
      if (!can("finance.settle")) return forbidden()
      if (bill.status !== "paid") return transition()
      const paid = s.entries.findLast(e => e.ref === `bill:${bill.id}` && e.kind === "settlement" && !e.reversal && !s.reversed.has(e.tx))
      if (paid) reverse(paid.tx)
      Object.assign(bill, {status: "forecast", paid_date: undefined, auto_settle: false})
      return ok(withBucket(bill))
    }
    if (action === "cancel") {
      if (!can("finance.write")) return forbidden()
      if (bill.status !== "forecast") return transition()
      bill.status = "canceled"
      return ok(bill)
    }
    if (method === "patch") {
      if (!can("finance.write")) return forbidden()
      if (bill.status !== "forecast") return transition()
      const p = body<Partial<Bill>>(r)
      if (p.auto_settle === true && !bill.auto_settle && !can("finance.settle")) return forbidden()
      Object.assign(bill, Object.fromEntries(Object.entries(p).filter(([, v]) => v !== undefined)))
      return ok(withBucket(bill))
    }
  }

  // recurrences
  if (path === "/recurrences" && method === "get") return ok({data: s.recurrences, has_more: false})
  if (path === "/recurrences/preview" && method === "post") {
    const p = body<{expression: ExpressionJSON; start: string; from?: string; count: number}>(r)
    if (!(p.count >= 1 && p.count <= 24)) {
      return {status: 422, data: {type: "about:blank", title: "Validation", status: 422, errors: [{field: "count", message: "entre 1 e 24"}]}}
    }
    const from = p.from && p.from > p.start ? p.from : p.start
    return ok({data: occurrences(p.expression, from, p.count), has_more: false})
  }
  if (path === "/recurrences" && method === "post") {
    if (!can("finance.write")) return forbidden()
    const p = body<Partial<Recurrence>>(r)
    if (p.auto_settle && !can("finance.settle")) return forbidden()
    const rec: Recurrence = {
      id: nextId("r"), direction: p.direction!, amount: p.amount!, category_id: p.category_id!, account_id: p.account_id!,
      description: p.description, expression: p.expression!, start: p.start!, end: p.end,
      business_day_adjust: p.business_day_adjust ?? "none", auto_settle: !!p.auto_settle, archived: false,
    }
    s.recurrences.push(rec)
    return ok(rec, 201)
  }
  if (recMatch) {
    const rec = s.recurrences.find(x => x.id === recMatch[1])
    if (!rec) return problem(404, "about:blank", "Not Found", "recurso não encontrado")
    if (!can("finance.write")) return forbidden()
    if (path.endsWith("/archive")) {
      rec.archived = true
      return {status: 204, data: ""}
    }
    if (method === "patch") {
      Object.assign(rec, Object.fromEntries(Object.entries(body<Partial<Recurrence>>(r)).filter(([, v]) => v !== undefined)))
      return ok(rec)
    }
  }

  // projection
  if (path === "/projection") {
    const n = Number(r.params?.months ?? 6)
    const data: ProjectionMonth[] = []
    for (let i = 0; i < n; i++) {
      const [y, m] = monthOffset(today, i)
      const month = `${y}-${String(m).padStart(2, "0")}`
      const open = s.bills.filter(b => b.status === "forecast" && (b.due_date.slice(0, 7) === month || (i === 0 && b.due_date.slice(0, 7) < month)))
      const sum = (d: Direction) => open.filter(b => b.direction === d).reduce((a, b) => a + b.amount, 0)
      // Beyond the two-month horizon, recurrences are virtual.
      const virtual = i >= 2 ? s.recurrences.filter(x => !x.archived).reduce((a, x) => a + (x.direction === "payable" ? -x.amount : x.amount), 0) : 0
      data.push({month, receivable: sum("receivable"), payable: sum("payable"), virtual})
    }
    return ok({data, has_more: false})
  }

  // accounts and settings
  if (path === "/accounts" && method === "get") return ok({data: s.accounts, has_more: false})
  if (path === "/accounts" && method === "post") {
    if (!can("finance.configure")) return forbidden()
    const p = body<{name: string; class: AccountClass; dre_group?: Account["dre_group"]}>(r)
    const a: Account = {id: nextId("a"), name: p.name, class: p.class, dre_group: p.dre_group, system: false, archived: false, balance: 0}
    s.accounts.push(a)
    return ok(a, 201)
  }
  const archive = path.match(/^\/accounts\/([^/]+)\/archive$/)
  if (archive) {
    if (!can("finance.configure")) return forbidden()
    const a = s.accounts.find(x => x.id === archive[1])
    if (!a) return problem(404, "about:blank", "Not Found", "recurso não encontrado")
    a.archived = true
    return {status: 204, data: ""}
  }
  // statement, transfers, reversals, reports
  const cash = (id: string) => s.accounts.some(a => a.id === id && a.class === "asset" && !a.system && !a.archived)
  const opening = path.match(/^\/accounts\/([^/]+)\/opening-balance$/)
  if (opening && method === "post") {
    if (!can("finance.configure")) return forbidden()
    const id = opening[1]
    if (!cash(id)) return problem(422, "about:blank", "Unprocessable", "conta ou categoria desconhecida neste espaço")
    if (s.openings.has(id)) return problem(409, "/problems/invalid-transition", "Invalid Transition", "Esta conta já tem saldo inicial. Estorne o atual para lançar outro.")
    const p = body<{amount: number; date: string}>(r)
    s.openings.add(id)
    return ok({transaction_id: post([{account: id, date: p.date, amount: p.amount, kind: "opening_balance", flow: "-", memo: "Saldo inicial"}])}, 201)
  }
  if (path === "/transfers" && method === "post") {
    if (!can("finance.write")) return forbidden()
    const p = body<{from_account_id: string; to_account_id: string; amount: number; date: string; memo?: string}>(r)
    if (!cash(p.from_account_id) || !cash(p.to_account_id) || p.from_account_id === p.to_account_id) {
      return problem(422, "about:blank", "Unprocessable", "conta ou categoria desconhecida neste espaço")
    }
    const memo = p.memo || "Transferência"
    return ok({transaction_id: post([
      {account: p.to_account_id, date: p.date, amount: p.amount, kind: "transfer", flow: "-", memo},
      {account: p.from_account_id, date: p.date, amount: -p.amount, kind: "transfer", flow: "-", memo},
    ])}, 201)
  }
  const rev = path.match(/^\/transactions\/([^/]+)\/reverse$/)
  if (rev && method === "post") {
    if (!can("finance.write")) return forbidden()
    const legs = s.entries.filter(e => e.tx === rev[1])
    if (!legs.length) return problem(404, "about:blank", "Not Found", "recurso não encontrado")
    if (legs[0].reversal || (legs[0].kind !== "transfer" && legs[0].kind !== "opening_balance")) {
      return problem(409, "/problems/invalid-transition", "Invalid Transition", "Só transferências e saldos iniciais são estornados pelo extrato. Para um pagamento, use Desfazer pagamento.")
    }
    if (s.reversed.has(rev[1])) return problem(409, "/problems/invalid-transition", "Invalid Transition", "ledger: transaction already reversed")
    if (legs[0].kind === "opening_balance") s.openings.delete(legs[0].account)
    return ok({transaction_id: reverse(rev[1])}, 201)
  }
  const stmt = path.match(/^\/accounts\/([^/]+)\/statement$/)
  if (stmt && method === "get") {
    const a = s.accounts.find(x => x.id === stmt[1] && x.class === "asset" && !x.system)
    if (!a) return problem(404, "about:blank", "Not Found", "recurso não encontrado")
    const from = String(r.params?.from), to = String(r.params?.to)
    const since = s.entries.filter(e => e.account === a.id && e.date >= from).sort((x, y) => x.date.localeCompare(y.date))
    let bal = a.balance - since.reduce((t, e) => t + e.amount, 0)
    const opening = bal
    const entries: StatementEntry[] = since.filter(e => e.date < to).map(e => ({
      transaction_id: e.tx, date: e.date, amount: e.amount, balance: (bal += e.amount), kind: e.kind, memo: e.memo,
      category_id: e.flow && e.flow !== "-" ? e.flow : undefined, bill_id: e.ref?.replace(/^bill:/, ""),
      reversal: !!e.reversal, reversed: s.reversed.has(e.tx),
    }))
    return ok({account_id: a.id, from, to, opening, closing: bal, entries})
  }
  if (path === "/reports/cash-flow" || path === "/reports/dre") {
    const from = String(r.params?.from), to = String(r.params?.to)
    const months: string[] = []
    for (let [y, m] = parts(`${from}-01`); `${y}-${String(m).padStart(2, "0")}` <= to; m === 12 ? (y++, m = 1) : m++) {
      months.push(`${y}-${String(m).padStart(2, "0")}`)
    }
    if (path === "/reports/dre") {
      const rows = new Map<string, number[]>()
      for (const b of s.bills.filter(x => x.status !== "canceled")) {
        const i = months.indexOf(b.competence_date.slice(0, 7))
        if (i < 0) continue
        const v = rows.get(b.category_id) ?? months.map(() => 0)
        v[i] += b.direction === "payable" ? -b.amount : b.amount
        rows.set(b.category_id, v)
      }
      const order = ["gross_revenue", "deductions", "costs", "operating_expenses", "financial_result", "other"] as const
      const sum = (xs: number[][]) => months.map((_, i) => xs.reduce((t, x) => t + x[i], 0))
      const groups: DREGroupLine[] = order.map(g => {
        const categories = [...rows].filter(([id]) => (s.accounts.find(a => a.id === id)?.dre_group ?? "other") === g)
          .map(([id, amounts]) => ({category_id: id, amounts, total: amounts.reduce((t, x) => t + x, 0)}))
        const amounts = sum(categories.map(c => c.amounts))
        return {group: g, categories, amounts, total: amounts.reduce((t, x) => t + x, 0)}
      }).filter(g => g.categories.length > 0)
      const result = sum(groups.map(g => g.amounts))
      return ok({months, groups, result, total: result.reduce((t, x) => t + x, 0)})
    }
    const cashIds = new Set(s.accounts.filter(a => a.class === "asset" && !a.system).map(a => a.id))
    const since = s.entries.filter(e => cashIds.has(e.account) && e.date >= `${from}-01`)
    const now = s.accounts.filter(a => cashIds.has(a.id)).reduce((t, a) => t + a.balance, 0)
    const opening_cash = now - since.reduce((t, e) => t + e.amount, 0)
    const out: CashFlowMonth[] = months.map(month => {
      const m: CashFlowMonth = {month, in: 0, out: 0, openings: 0, lines: []}
      const by = new Map<string, number>()
      for (const e of since.filter(x => x.date.slice(0, 7) === month)) {
        if (e.kind === "opening_balance") m.openings += e.amount
        else if (e.flow !== "-") {
          if ((e.reversal ? -e.amount : e.amount) > 0) m.in += e.amount
          else m.out -= e.amount
          by.set(e.flow, (by.get(e.flow) ?? 0) + e.amount)
        }
      }
      m.lines = [...by].filter(([, a]) => a !== 0).map(([category_id, amount]) => ({category_id, amount}))
      return m
    })
    const closing_cash = out.reduce((t, m) => t + m.in - m.out + m.openings, opening_cash)
    return ok({from, to, opening_cash, closing_cash, months: out})
  }

  if (path === "/settings" && method === "get") return ok({default_receiving_account_id: s.defaultReceiving})
  if (path === "/settings/default-receiving-account") {
    if (!can("finance.configure")) return forbidden()
    s.defaultReceiving = body<{default_receiving_account_id: string}>(r).default_receiving_account_id
    return ok({default_receiving_account_id: s.defaultReceiving})
  }

  return problem(404, "about:blank", "Not Found", "rota de finanças desconhecida no mock")
}

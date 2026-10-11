import type {AxiosAdapter, AxiosRequestConfig, AxiosResponse} from "axios"

import type {Invoice} from "@/lib/api/types"
import {
  DEFAULT_SCENARIO,
  MOCK_CUSTOMER,
  MOCK_CUSTOMER_PENDING_TERMS,
  type MockScenario,
} from "@/lib/mockConfig"
import {
  consoleCustomerDetail,
  consoleCustomers,
  consoleOverview,
  consoleSettings,
  consoleInvoiceDetail,
  consoleInvoices,
  consoleProduct,
  consoleProducts,
  consoleSubscriptionDetail,
  consoleSubscriptions,
  consoleSession,
  MOCK_EVENT_TYPES,
  MOCK_MACHINE_SCOPES,
  mockCredentials,
  mockWebhooks,
  MOCK_COMPANIES,
  MOCK_MERCHANT_ORG,
  MOCK_NONE_ORG,
  mockApplication,
  setConsoleDunning,
  setConsoleIssuer,
} from "./consoleMockData"
import {financeMock} from "./financeMockData"
import {FIXTURES, MOCK_PIX_CODE} from "./mockData"

/** The one organization the portal mock lets a person manage. */
const MOCK_PORTAL_ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

const STORAGE_KEY = "ctech-billing-mock-scenario"

/** The scenario is per-tab state, not module state: reading it fresh on every
 *  request is what lets MockControls switch fixtures without a reload. */
export function currentScenario(): MockScenario {
  if (typeof window === "undefined") return DEFAULT_SCENARIO
  // `?scenario=` is promoted to storage rather than merely read. Client-side
  // navigation drops the query string, so a scenario that only lived in the
  // URL silently reverted on the first link — leaving the switcher showing one
  // fixture while the requests served another.
  const fromUrl = new URLSearchParams(window.location.search).get("scenario")
  if (fromUrl && fromUrl in FIXTURES) window.localStorage.setItem(STORAGE_KEY, fromUrl)
  const stored = window.localStorage.getItem(STORAGE_KEY)
  return stored && stored in FIXTURES ? (stored as MockScenario) : DEFAULT_SCENARIO
}

export function setScenario(scenario: MockScenario) {
  window.localStorage.setItem(STORAGE_KEY, scenario)
  window.dispatchEvent(new CustomEvent("ctech-mock-scenario", {detail: scenario}))
}

/** Invoices the mock has mutated this session (a payment settling, say),
 *  keyed by id. Cleared whenever the scenario changes. */
/** Set once the mocked accept succeeds, so the gate does not come back on
 *  every navigation within the tab. */
let termsAccepted = false

const overrides = new Map<string, Invoice>()
let overridesFor: MockScenario | null = null

function fixture() {
  const scenario = currentScenario()
  if (overridesFor !== scenario) {
    overrides.clear()
    overridesFor = scenario
  }
  return {scenario, ...FIXTURES[scenario]}
}

function invoices(): Invoice[] {
  return fixture().invoices.map(inv => overrides.get(inv.id) ?? inv)
}

/** Marks an invoice settled. Called by the mock payment stream when its timer
 *  fires, so a refetch after "pago" returns the paid invoice rather than the
 *  open one the fixture still describes. */
export function settleInvoice(id: string) {
  const inv = invoices().find(i => i.id === id)
  if (!inv) return
  overrides.set(id, {
    ...inv,
    state: "paid",
    tone: "positive",
    amount_paid: inv.total,
    amount_due: 0,
    payable: false,
  })
}

export function settleAfterSeconds() {
  return fixture().settleAfterSeconds
}

const wait = (ms: number) => new Promise(resolve => setTimeout(resolve, ms))

/** Who the checkout page says is being paid. On the real thing it comes from
 *  the organization behind the token. */
const MOCK_MERCHANT = "CTech Tecnologia"

/**
 * An Invoice narrowed to what `checkoutResponse` actually carries.
 *
 * Written as a projection rather than by handing the fixture straight through,
 * because the point of that DTO is what it leaves out (ADR 0009 § minimization)
 * — a mock that returns the whole invoice would let a screen read a field the
 * server never sends and pass every local test.
 */
function checkoutView(inv: Invoice) {
  return {
    number: inv.number,
    description: inv.description,
    ...(inv.extra_lines ? {extra_lines: inv.extra_lines} : {}),
    state: inv.state,
    tone: inv.tone,
    days_until_due: inv.days_until_due,
    due_date: inv.due_date,
    amount_due: inv.amount_due,
    currency: inv.currency,
    lines: inv.lines,
    payable: inv.payable,
  }
}

function ok<T>(config: AxiosRequestConfig, data: T, status = 200): AxiosResponse<T> {
  return {data, status, statusText: "OK", headers: {}, config: config as never}
}

function fail(
  config: AxiosRequestConfig,
  status: number,
  title: string,
  detail: string,
  type = "about:blank",
  code?: string,
  errors?: unknown
): never {
  const error = new Error(title) as Error & {
    isAxiosError: boolean
    response: AxiosResponse
    config: AxiosRequestConfig
    code?: string
  }
  error.isAxiosError = true
  error.config = config
  error.response = {
    data: {type, title, status, detail, code, errors},
    status,
    statusText: title,
    headers: {},
    config: config as never,
  }
  throw error
}

function networkDown(config: AxiosRequestConfig): never {
  const error = new Error("Network Error") as Error & { isAxiosError: boolean; code: string }
  error.isAxiosError = true
  error.code = "ERR_NETWORK"
  ;(error as unknown as { config: AxiosRequestConfig }).config = config
  throw error
}

/**
 * An axios adapter, not a mock server and not MSW.
 *
 * It substitutes the transport and nothing above it, so every screen, hook,
 * query key and error path exercised here is the same code that will talk to
 * the real API. A mock that intercepts higher up proves the fixtures render;
 * this one also proves the client does.
 */
export const mockAdapter: AxiosAdapter = async config => {
  const url = config.url ?? ""
  const method = (config.method ?? "get").toLowerCase()
  const {scenario, pixTtlSeconds} = fixture()

  // Enough latency for a skeleton to be visible and be designed. A mock that
  // answers instantly is a mock in which no loading state is ever wrong.
  await wait(220 + Math.random() * 180)

  if (scenario === "erro_de_rede") networkDown(config)
  if (scenario === "manutencao") {
    fail(config, 503, "Em manutenção", "o serviço de cobranças está temporariamente indisponível", "about:blank", "service_unavailable")
  }

  // Answered before anything else that could 404: /maintenance polls this to
  // find out whether the service is back, and the `manutencao` scenario above
  // is what makes it fail.
  if (url.endsWith("/v1.0/health")) return ok(config, {status: "pass"})

  // The portal selector's list is mounted before identity, like the real one:
  // an admin with no account of their own still gets it.
  if (url.endsWith("/v1.0/portal/spaces")) {
    return ok(config, {
      spaces: [
        {selector: "personal", display_name: "Pessoal"},
        {selector: `org:${MOCK_PORTAL_ORG}`, display_name: "Acme Comércio e Serviços LTDA", role: "admin"},
      ],
      organizations_unavailable: false,
    })
  }
  const portalOrg = String(config.headers?.["X-Billing-Space"] ?? "").startsWith("org:")

  // Above every portal route, which is where the real refusal is: identity is
  // resolved in middleware, so no handler below it is reachable.
  if (((scenario === "sem_conta" && !portalOrg) || (scenario === "org_sem_conta" && portalOrg)) && url.includes("/v1.0/portal/")) {
    fail(
      config,
      403,
      "Forbidden",
      "nenhuma conta de cobrança para este usuário",
      "/problems/no-billing-account",
      "no_billing_account"
    )
  }

  // ── Finance ────────────────────────────────────────────────────────────────
  // Above the console block, because `sem_conta` (no organization) still has a
  // personal finance space. The module enforces the space, mode and
  // idempotency headers itself.
  if (url.includes("/v1.0/console/finance/")) {
    const res = financeMock({
      method, url, headers: config.headers as Record<string, unknown>, data: config.data,
      params: config.params as Record<string, unknown>,
    })
    if (res.status >= 400) {
      const p = res.data as {type?: string; title?: string; detail?: string; code?: string; errors?: unknown}
      fail(config, res.status, p.title ?? "Error", p.detail ?? "", p.type, p.code, p.errors)
    }
    return {...ok(config, res.data), status: res.status}
  }

  // ── Console ────────────────────────────────────────────────────────────────
  // Answered above the portal routes and keyed on the mode header, which is the
  // one thing the console adds to every request: a fixture that ignored it
  // would make the mode switch look broken in exactly the mode where being
  // wrong is expensive.
  if (url.includes("/v1.0/console/")) {
    if (url.endsWith("/v1.0/console/session")) {
      if (scenario === "sem_conta") return ok(config, {organizations: [], organizations_unavailable: false})
      return ok(config, consoleSession(scenario))
    }
    // The billing half, like the server: mode and organization required, and
    // only the merchant organization has a tenant.
    const selector = String(config.headers?.["X-Billing-Space"] ?? "")
    if (!selector.startsWith("org:") || !config.headers?.["X-Billing-Mode"]) {
      fail(config, 400, "Bad Request", "informe X-Billing-Mode e X-Billing-Space", "about:blank", "bad_request")
    }
    // Part 2: the application runs without a tenant, for any organization.
    if (url.includes("/v1.0/console/merchant/")) {
      if (url.endsWith("/merchant/companies")) return ok(config, {data: MOCK_COMPANIES})
      if (method === "post") return ok(config, mockApplication("pending"), 201)
      if (scenario === "pedido_em_analise") return ok(config, mockApplication("pending"))
      if (scenario === "pedido_recusado") return ok(config, mockApplication("rejected"))
      fail(config, 404, "Not Found", "this organization has not applied", "about:blank", "no_application")
    }
    // Tenant zero's review: the merchant organization plays tenant zero.
    if (url.includes("/v1.0/console/applications")) {
      if (selector !== `org:${MOCK_MERCHANT_ORG}`) fail(config, 404, "Not Found", "resource not found", "about:blank", "resource_not_found")
      if (method === "post") {
        const decided = url.endsWith("/approve") ? "approved" : "rejected"
        return ok(config, {...mockApplication(decided), status: decided, decided_at: new Date().toISOString()})
      }
      const status = String((config.params as {status?: string})?.status ?? "pending")
      return ok(config, {data: status === "pending" ? [mockApplication("pending")] : [], has_more: false})
    }
    // Padaria has a test tenant once it applied (in review or rejected).
    const padariaTest = selector === `org:${MOCK_NONE_ORG}` && config.headers?.["X-Billing-Mode"] === "test" &&
      (scenario === "pedido_em_analise" || scenario === "pedido_recusado")
    if (selector !== `org:${MOCK_MERCHANT_ORG}` && !padariaTest) {
      fail(config, 404, "No merchant", "this organization does not bill through CTech Billing in this mode", "/problems/no-merchant", "no_merchant")
    }
    const mode = (config.headers?.["X-Billing-Mode"] as string) === "test" ? "test" : "live"
    const invoices = consoleInvoices(mode)

    if (url.endsWith("/v1.0/console/credentials") && method === "get") {
      return ok(config, {data: mockCredentials, machine_scopes: MOCK_MACHINE_SCOPES})
    }
    if (url.endsWith("/v1.0/console/credentials") && method === "post") {
      const clientId = String(JSON.parse(String(config.data ?? "{}")).client_id ?? "")
      if (!clientId.startsWith("cli_")) fail(config, 422, "Unprocessable Entity", "the client cannot be connected", "about:blank", "client_not_yours")
      const cred = {client_id: clientId, livemode: mode === "live", active: true, provisioned: false, connected_by: "usr_mock", connected_by_me: true, connected_at: new Date().toISOString()}
      mockCredentials.push(cred)
      return ok(config, cred, 201)
    }
    if (url.includes("/v1.0/console/credentials/") && method === "delete") {
      const clientId = decodeURIComponent(url.split("/v1.0/console/credentials/")[1])
      const i = mockCredentials.findIndex(c => c.client_id === clientId)
      if (i >= 0 && mockCredentials[i].provisioned) fail(config, 409, "Conflict", "managed by CTech", "about:blank", "credential_provisioned")
      if (i >= 0) mockCredentials.splice(i, 1)
      return ok(config, undefined, 204)
    }
    if (url.endsWith("/v1.0/console/webhooks") && method === "get") {
      return ok(config, {data: mockWebhooks, event_types: MOCK_EVENT_TYPES, max_endpoints: 5})
    }
    if (url.endsWith("/v1.0/console/webhooks") && method === "post") {
      if (mockWebhooks.length >= 5) fail(config, 422, "Unprocessable Entity", "limit", "about:blank", "webhook_endpoint_limit")
      const body = JSON.parse(String(config.data ?? "{}")) as {url: string; events: string[]}
      const created = {id: `whe_mock${mockWebhooks.length}`, url: body.url, events: body.events, status: "active" as const, failure_run: 0, provisioned: false, added_at: new Date().toISOString()}
      mockWebhooks.push(created)
      return ok(config, {...created, secret: `whsec_${"ab".repeat(32)}`}, 201)
    }
    const hook = url.match(/\/v1\.0\/console\/webhooks\/([^/?]+)(\/rotate-secret|\/test)?$/)
    if (hook) {
      const e = mockWebhooks.find(w => w.id === decodeURIComponent(hook[1]))
      if (!e) fail(config, 404, "Not Found", "resource not found", "about:blank", "resource_not_found")
      if (e!.provisioned) fail(config, 409, "Conflict", "managed by CTech", "about:blank", "webhook_endpoint_provisioned")
      if (hook[2] === "/rotate-secret") return ok(config, {...e!, secret: `whsec_${"cd".repeat(32)}`})
      if (hook[2] === "/test") return ok(config, {event_id: "evt_mock"}, 202)
      if (method === "delete") {
        mockWebhooks.splice(mockWebhooks.indexOf(e!), 1)
        return ok(config, undefined, 204)
      }
    }
    if (url.includes("/v1.0/console/overview")) {
      const params = new URLSearchParams(url.split("?")[1] ?? "")
      const today = new Date()
      return ok(config, consoleOverview(
        mode,
        Number(params.get("year")) || today.getFullYear(),
        Number(params.get("month")) || today.getMonth() + 1,
      ))
    }
    if (url.endsWith("/v1.0/console/settings")) {
      return ok(config, consoleSettings(mode))
    }
    if (url.endsWith("/v1.0/console/settings/dunning") && method === "put") {
      const body = JSON.parse((config.data as string) || "{}")
      return ok(config, setConsoleDunning(body.steps ?? []))
    }
    const productDunning = url.match(/\/v1\.0\/console\/products\/([^/]+)\/dunning$/)
    if (productDunning && method === "put") {
      const body = JSON.parse((config.data as string) || "{}")
      const steps = body.steps ?? []
      return ok(config, {steps: steps.length > 0 ? steps : consoleSettings(mode).dunning.steps, custom: steps.length > 0})
    }
    if (url.endsWith("/v1.0/console/settings/issuer") && method === "put") {
      return ok(config, setConsoleIssuer(JSON.parse((config.data as string) || "{}")))
    }
    // The document link. A real one is a presigned S3 URL the API signs after
    // rendering; the fixture points at a data: URL so the button's whole path —
    // pending label, navigation, error toast — is exercised without S3.
    const pdf = url.match(/\/v1\.0\/(console|portal)\/invoices\/([^/]+)\/pdf$/)
    if (pdf) {
      return ok(config, {
        url: "data:application/pdf;base64,JVBERi0xLjQK",
        expires_in: 300,
      })
    }

    const taxID = url.match(/\/v1\.0\/console\/customers\/([^/]+)\/tax-id$/)
    if (taxID && method === "post") {
      return ok(config, {tax_id: "123.456.789-09"})
    }
    if (url.includes("/v1.0/console/invoices?") || url.endsWith("/v1.0/console/invoices")) {
      return ok(config, {data: invoices, has_more: false})
    }

    const write = url.match(/\/v1\.0\/console\/invoices\/([^/]+)\/(finalize|void|credit-notes)$/)
    if (write && method === "post") {
      const inv = invoices.find(i => i.id === write[1])
      if (!inv) fail(config, 404, "Não encontrada", "fatura não encontrada", "about:blank", "invoice_not_found")
      switch (write[2]) {
        case "finalize":
          // Mutating the fixture in place is what makes the screen re-render
          // into the state the real API would have returned. It lasts as long
          // as the tab, which is the right lifetime for a mock.
          if (inv.status === "DRAFT") {
            inv.status = "OPEN"
            inv.number = 1045
          }
          return ok(config, consoleInvoiceDetail(inv))
        case "void":
          inv.status = "VOID"
          inv.amount_due = 0
          return ok(config, consoleInvoiceDetail(inv))
        default:
          return ok(config, {id: "cn_mock_new"}, 201)
      }
    }

    const detail = url.match(/\/v1\.0\/console\/invoices\/([^/]+)$/)
    if (detail) {
      const inv = invoices.find(i => i.id === detail[1])
      if (!inv) fail(config, 404, "Não encontrada", "fatura não encontrada", "about:blank", "invoice_not_found")
      return ok(config, consoleInvoiceDetail(inv))
    }

    if (url.endsWith("/v1.0/console/customers") && method === "get") {
      return ok(config, {data: consoleCustomers(mode), has_more: false})
    }
    const customer = url.match(/\/v1\.0\/console\/customers\/([^/]+)$/)
    if (customer) {
      const found = consoleCustomers(mode).find(c => c.id === customer[1])
      if (!found) fail(config, 404, "Não encontrado", "cliente não encontrado", "about:blank", "resource_not_found")
      return ok(config, consoleCustomerDetail(found))
    }

    if (url.endsWith("/v1.0/console/subscriptions") && method === "get") {
      return ok(config, {data: consoleSubscriptions(mode), has_more: false})
    }
    const subscription = url.match(/\/v1\.0\/console\/subscriptions\/([^/]+)$/)
    if (subscription) {
      const found = consoleSubscriptions(mode).find(s => s.id === subscription[1])
      if (!found) fail(config, 404, "Não encontrada", "assinatura não encontrada", "about:blank", "subscription_not_found")
      return ok(config, consoleSubscriptionDetail(found))
    }
    const subCancel = url.match(/\/v1\.0\/console\/subscriptions\/([^/]+)\/cancel$/)
    if (subCancel && method === "post") {
      const found = consoleSubscriptions(mode).find(s => s.id === subCancel[1])
      if (!found) fail(config, 404, "Não encontrada", "assinatura não encontrada", "about:blank", "subscription_not_found")
      const atPeriodEnd = JSON.parse((config.data as string) || "{}").at_period_end === true
      if (atPeriodEnd) {
        found.cancel_at_period_end = true
      } else {
        found.status = "CANCELED"
        found.entitled = false
      }
      return ok(config, found)
    }

    if (url.endsWith("/v1.0/console/products") && method === "get") {
      return ok(config, {data: consoleProducts(), has_more: false})
    }
    const product = url.match(/\/v1\.0\/console\/products\/([^/]+)$/)
    if (product && method === "get") {
      const found = consoleProduct(product[1])
      if (!found) fail(config, 404, "Não encontrado", "produto não encontrado", "about:blank", "resource_not_found")
      return ok(config, found)
    }
    // The catalogue writes answer with a plausible row rather than mutating the
    // fixtures: what these screens need designed is the dialog and the toast,
    // and a mock catalogue that grew every time somebody clicked would make the
    // list screen useless within a session.
    if (url.endsWith("/v1.0/console/products") && method === "post") {
      return ok(config, {id: "prod_mock_novo", name: "Novo produto", active: true, livemode: true}, 201)
    }
    if (url.endsWith("/v1.0/console/prices") && method === "post") {
      return ok(config, {id: "price_mock_novo"}, 201)
    }
    const archive = url.match(/\/v1\.0\/console\/prices\/([^/]+)\/archive$/)
    if (archive && method === "post") {
      return ok(config, {id: archive[1], archived: true})
    }

    fail(config, 404, "Rota não mockada", `sem fixture para ${method.toUpperCase()} ${url}`, "about:blank", "resource_not_found")
  }

  if (url.endsWith("/v1.0/portal/session")) {
    return ok(config, scenario === "termos_pendentes" && !termsAccepted ? MOCK_CUSTOMER_PENDING_TERMS : MOCK_CUSTOMER)
  }

  // Accepting flips the session for the rest of the tab, so the gate dismisses
  // and the portal behind it renders — which is the half of this flow that a
  // static fixture cannot show.
  if (url.endsWith("/v1.0/portal/terms/accept") && method === "post") {
    termsAccepted = true
    return ok(config, MOCK_CUSTOMER)
  }

  if (url.endsWith("/v1.0/portal/subscriptions")) {
    // The organization's own, so a switch visibly changes the screen.
    return ok(config, {data: portalOrg ? fixture().subscriptions.slice(0, 1) : fixture().subscriptions, has_more: false})
  }

  // The detail carries the plan's own invoice history, which the list route
  // deliberately does not — same split as the real API.
  const subDetail = url.match(/\/v1\.0\/portal\/subscriptions\/([^/]+)$/)
  if (subDetail) {
    const sub = fixture().subscriptions.find(s => s.id === subDetail[1])
    if (!sub) fail(config, 404, "Não encontrada", "assinatura não encontrada", "about:blank", "subscription_not_found")
    return ok(config, {...sub, recent_invoices: invoices().filter(i => !i.payable)})
  }

  const cancel = url.match(/\/v1\.0\/portal\/subscriptions\/([^/]+)\/cancel$/)
  if (cancel && method === "post") {
    const sub = fixture().subscriptions.find(s => s.id === cancel[1])
    if (!sub) fail(config, 404, "Não encontrado", "assinatura não encontrada", "about:blank", "subscription_not_found")
    return ok(config, {
      ...sub,
      state: "active_until_period_end",
      tone: "attention",
      renews_on: undefined,
    })
  }

  if (url.endsWith("/v1.0/portal/invoices")) {
    return ok(config, {data: portalOrg ? invoices().slice(0, 2) : invoices(), has_more: false})
  }

  const pay = url.match(/\/v1\.0\/portal\/invoices\/([^/]+)\/pay$/)
  if (pay && method === "post") {
    const inv = invoices().find(i => i.id === pay[1])
    if (!inv) fail(config, 404, "Não encontrado", "fatura não encontrada", "about:blank", "invoice_not_found")
    if (!inv.payable) {
      fail(config, 422, "Não pagável", "esta fatura não está aberta para pagamento", "about:blank", "invoice_not_payable")
    }
    return ok(config, {
      invoice: inv,
      payment: {
        method: "pix",
        pix_code: MOCK_PIX_CODE,
        expires_at: new Date(Date.now() + pixTtlSeconds * 1000).toISOString(),
      },
    })
  }

  // The payment link, X1. Any token resolves to the first payable invoice —
  // the real one is an HMAC the fixtures cannot produce, and what this exists
  // to exercise is the screen, not the signature. `MOCK_CHECKOUT_TOKEN` is the
  // one the switcher links to.
  const checkoutPay = url.match(/\/v1\.0\/checkout\/([^/]+)\/pay$/)
  if (checkoutPay && method === "post") {
    const inv = invoices().find(i => i.payable)
    if (!inv) fail(config, 404, "Not Found", "invalid or expired link", "/problems/invalid-link", "invalid_link")
    return ok(config, {
      merchant: MOCK_MERCHANT,
      invoice: checkoutView(inv),
      payment: {
        method: "pix",
        pix_code: MOCK_PIX_CODE,
        expires_at: new Date(Date.now() + pixTtlSeconds * 1000).toISOString(),
      },
    })
  }

  const checkout = url.match(/\/v1\.0\/checkout\/([^/]+)$/)
  if (checkout) {
    const inv = invoices().find(i => i.payable) ?? invoices()[0]
    if (!inv) fail(config, 404, "Not Found", "invalid or expired link", "/problems/invalid-link", "invalid_link")
    return ok(config, {merchant: MOCK_MERCHANT, invoice: checkoutView(inv)})
  }

  const portalPDF = url.match(/\/v1\.0\/portal\/invoices\/([^/]+)\/pdf$/)
  if (portalPDF) {
    return ok(config, {url: "data:application/pdf;base64,JVBERi0xLjQK", expires_in: 300})
  }

  const detail = url.match(/\/v1\.0\/portal\/invoices\/([^/]+)$/)
  if (detail) {
    const inv = invoices().find(i => i.id === detail[1])
    if (!inv) fail(config, 404, "Não encontrada", "fatura não encontrada", "about:blank", "invoice_not_found")
    return ok(config, inv)
  }

  fail(config, 404, "Rota não mockada", `sem fixture para ${method.toUpperCase()} ${url}`, "about:blank", "resource_not_found")
}

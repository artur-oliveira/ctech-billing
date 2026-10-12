import type {Connection, OnboardingStep, ProviderInfo, UnclassifiedLine} from "@/lib/api/connectionsTypes"

/**
 * Bank connections for `npm run dev:mock` (BYOK spec 2026-10-11). Stateful per
 * organization and mode, like the API: onboarding is remembered, a live
 * connect needs the form, a test connect turns on the shared sandbox, sync
 * now is refused within ten minutes.
 */
type Req = {method?: string; url: string; headers?: Record<string, unknown>; data?: unknown}
type Res = {status: number; data: unknown}

export const MOCK_BYOK_ACCOUNT = "byok-inter-0a1b2c3d4e5f"

const PROVIDERS: ProviderInfo[] = [{
  provider: "inter",
  fields: [
    {name: "client_id", required: true}, {name: "client_secret", secret: true, required: true},
    {name: "cert", file: true, required: true}, {name: "key", file: true, secret: true, required: true},
    {name: "account", required: true}, {name: "pix_key", required: false},
  ],
  scopes: {statement: ["extrato.read"], collection: ["cobv.read", "cobv.write", "pix.read", "pix.write", "webhook.read", "webhook.write"]},
}]

const state = new Map<string, Connection>()
const lastSync = new Map<string, number>()
let unclassified: UnclassifiedLine[] = []

export function resetConnectionsMock() {
  state.clear()
  lastSync.clear()
  unclassified = seedLines()
}

function seedLines(): UnclassifiedLine[] {
  return [
    {tx_id: "tx_mock_1", date: "2026-10-09", amount: 125000, direction: "credit", memo: "Pix recebido · Cliente Alfa"},
    {tx_id: "tx_mock_2", date: "2026-10-08", amount: 4590, direction: "debit", memo: "Tarifa Pix"},
    {tx_id: "tx_mock_3", date: "2026-10-05", amount: 89900, direction: "debit", memo: "Pagamento de boleto · Aluguel"},
  ]
}
unclassified = seedLines()

const ok = (data: unknown, status = 200): Res => ({status, data})
const problem = (status: number, code: string, detail = code): Res => ({status, data: {status, code, detail, title: code}})

function header(r: Req, name: string): string {
  return String(r.headers?.[name] ?? r.headers?.[name.toLowerCase()] ?? "")
}

function iso(daysFromNow: number): string {
  return new Date(Date.now() + daysFromNow * 86_400_000).toISOString()
}

function active(live: boolean): Connection {
  return {
    provider: "inter", state: "active", status: "active", kind: live ? "merchant" : "sandbox_shared", livemode: live,
    client_id: live ? "a1b2c3d4-0000-4e5f-8a9b-mockclientid" : undefined, account_last4: live ? "4567" : undefined,
    cert_subject: live ? "CN=ACME SERVICOS LTDA:12345678000199" : undefined,
    cert_fingerprint: live ? "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" : undefined,
    cert_not_after: live ? iso(64) : undefined, cert_days_left: live ? 64 : undefined,
    capabilities: ["statement", "collection"], missing_scopes: [], last_sync_ok_at: iso(-0.02),
    ledger_account_id: MOCK_BYOK_ACCOUNT, backfill_done: true, first_day: "2026-07-11", connected_at: iso(-3),
    notify_email: live ? "financeiro@acme.com.br" : undefined,
  }
}

export function connectionsMock(r: Req): Res {
  const method = (r.method ?? "get").toLowerCase()
  const url = r.url.split("?")[0]
  if (url.includes("/v1.0/console/finance/accounts/") && url.endsWith("/unclassified")) return ok({data: unclassified})
  if (url.includes("/v1.0/console/finance/unclassified/") && url.endsWith("/classify") && method === "post") {
    const tx = url.split("/unclassified/")[1].split("/")[0]
    unclassified = unclassified.filter(l => l.tx_id !== tx)
    return ok({tx_id: `${tx}_classified`})
  }
  const mode = header(r, "X-Billing-Mode")
  const selector = header(r, "X-Billing-Space")
  if (!mode || !selector.startsWith("org:")) return problem(404, "space_not_found")
  const live = mode === "live"
  const path = url.replace(/^.*\/v1\.0\/console\/connections/, "")
  const key = `${selector}|${mode}`
  if (path === "" && method === "get") {
    const c = state.get(key)
    return ok({data: c ? [c] : [], providers: PROVIDERS})
  }
  const [, provider, action] = path.split("/")
  if (provider !== "inter") return problem(404, "resource_not_found")
  const cur = state.get(key)
  if (method !== "get" && !header(r, "Idempotency-Key")) return problem(400, "idempotency_key_required")
  switch (`${method} ${action ?? ""}`) {
    case "get ":
      return cur ? ok(cur) : problem(404, "byok_not_connected")
    case "put ": {
      if (cur && cur.state !== "onboarding") return problem(409, "byok_already_connected")
      if (live && !(r.data instanceof FormData)) return problem(422, "byok_invalid_request")
      if (live && r.data instanceof FormData && String(r.data.get("client_secret")).includes("errado")) return problem(422, "byok_credentials_refused")
      const c = {...active(live), backfill_done: !live}
      state.set(key, c)
      return ok(c, 201)
    }
    case "put onboarding": {
      const step = (r.data as {step?: OnboardingStep})?.step
      const base: Connection = cur ?? {provider: "inter", state: "onboarding", status: "onboarding", livemode: live, capabilities: [], missing_scopes: [], backfill_done: false}
      state.set(key, {...base, onboarding_step: step, onboarding_since: base.onboarding_step === step ? base.onboarding_since : new Date().toISOString()})
      return ok(null, 204)
    }
    case "post test":
      return cur ? ok({...cur, last_error_code: undefined}) : problem(404, "byok_not_connected")
    case "post certificate":
      if (!cur) return problem(404, "byok_not_connected")
      state.set(key, {...cur, cert_not_after: iso(365), cert_days_left: 365, state: "active", status: "active"})
      return ok(state.get(key))
    case "post sync": {
      if (!cur) return problem(404, "byok_not_connected")
      const last = lastSync.get(key) ?? 0
      if (Date.now() - last < 10 * 60_000) return problem(429, "byok_sync_too_soon")
      lastSync.set(key, Date.now())
      state.set(key, {...cur, last_sync_ok_at: new Date().toISOString(), backfill_done: true})
      return ok({posted: 2, skipped: 14, backfill_done: true}, 202)
    }
    case "delete ":
      state.delete(key)
      return ok(null, 204)
  }
  return problem(404, "resource_not_found")
}

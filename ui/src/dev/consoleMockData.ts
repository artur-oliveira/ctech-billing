import type {
  AuditEntry,
  ConsoleCustomer,
  ConsoleCustomerDetail,
  ConsoleInvoice,
  ConsoleInvoiceDetail,
  ConsoleInvoiceRow,
  ConsolePrice,
  ConsoleProduct,
  ConsoleSession,
  ConsoleSubscription,
  ConsoleSubscriptionDetail,
  DunningPolicy,
  DunningStep,
} from "@/lib/api/consoleTypes"

/**
 * Console fixtures.
 *
 * Kept apart from the portal's scenarios rather than folded into them: the two
 * shells answer different questions, and a scenario named "vence em 3 dias" is
 * a consumer's state, not an operator's. What an operator needs to see designed
 * is the *mix* — a draft that never got issued, an open bill, a paid one, one
 * that was written off — on one screen at once, which is a single fixture
 * rather than a switch.
 *
 * Test mode returns a different, smaller set on purpose: a mode switch that
 * changes nothing on screen is a control nobody trusts.
 */
const day = (offset: number): string => {
  const d = new Date()
  d.setDate(d.getDate() + offset)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
}

const iso = (offset: number): string => {
  const d = new Date()
  d.setDate(d.getDate() + offset)
  return d.toISOString()
}

export const MOCK_CONSOLE_SESSION: Record<"live" | "test", ConsoleSession> = {
  live: {
    organization_id: "org_mock_ctech",
    display_name: "CTech Tecnologia",
    livemode: true,
    payout_status: "enabled",
    can_charge: true,
  },
  test: {
    organization_id: "org_mock_ctech",
    display_name: "CTech Tecnologia",
    livemode: false,
    payout_status: "enabled",
    can_charge: true,
  },
}

function invoice(over: Partial<ConsoleInvoiceRow> & Pick<ConsoleInvoice, "id" | "status">): ConsoleInvoiceRow {
  const total = over.total ?? 11300
  return {
    number: 1042,
    customer_id: "cus_mock_ana",
    customer_name: "Ana Ribeiro",
    subscription_id: "sub_mock_ana",
    overdue: false,
    period: {start: day(-42), end: day(-13)},
    due_date: day(-27),
    currency: "BRL",
    subtotal: total,
    discount: 0,
    total,
    amount_paid: 0,
    amount_due: total,
    attempt_count: 0,
    livemode: true,
    lines: [
      {
        description: "Plano Essencial · mensal",
        period: {start: day(-42), end: day(-13)},
        quantity: 1,
        unit_amount: 8900,
        amount: 8900,
        proration: false,
      },
      {
        description: "Emissões adicionais (120 documentos)",
        period: {start: day(-42), end: day(-13)},
        quantity: 120,
        unit_amount: 20,
        amount: 2400,
        proration: false,
      },
    ],
    ...over,
  }
}

const LIVE: ConsoleInvoiceRow[] = [
  invoice({
    id: "in_mock_0044",
    number: 1044,
    status: "OPEN",
    overdue: true,
    due_date: day(-6),
    period: {start: day(-36), end: day(-7)},
    checkout_url: "https://billing.aoctech.app/checkout?token=mock",
  }),
  invoice({
    id: "in_mock_0043",
    number: 1043,
    status: "PAID",
    amount_paid: 11300,
    amount_due: 0,
    attempt_count: 1,
  }),
  invoice({
    id: "in_mock_0042",
    number: 1042,
    status: "UNCOLLECTIBLE",
    total: 9900,
    subtotal: 9900,
    amount_due: 9900,
    attempt_count: 4,
    due_date: day(-48),
  }),
  invoice({
    id: "in_mock_0045",
    status: "DRAFT",
    number: undefined,
    total: 8900,
    subtotal: 8900,
    amount_due: 8900,
    due_date: day(2),
    lines: [
      {
        description: "Plano Essencial · mensal",
        period: {start: day(-12), end: day(18)},
        quantity: 1,
        unit_amount: 8900,
        amount: 8900,
        proration: false,
      },
    ],
  }),
]

const TEST: ConsoleInvoiceRow[] = [
  invoice({
    id: "in_mock_test_1",
    number: 3,
    status: "OPEN",
    livemode: false,
    total: 100,
    subtotal: 100,
    amount_due: 100,
    due_date: day(5),
    lines: [
      {
        description: "Plano de testes",
        period: {start: day(-25), end: day(5)},
        quantity: 1,
        unit_amount: 100,
        amount: 100,
        proration: false,
      },
    ],
  }),
]

function timelineFor(inv: ConsoleInvoice): AuditEntry[] {
  const trail: AuditEntry[] = [
    {
      id: "aud_1",
      action: "invoice.created",
      cause: "scheduler",
      actor: "scheduler",
      created_at: iso(-43),
      request_id: "req_mock_create",
    },
  ]
  if (inv.status !== "DRAFT") {
    trail.push({
      id: "aud_2",
      action: "invoice.finalized",
      cause: "scheduler",
      actor: "scheduler",
      before: "DRAFT",
      after: "OPEN",
      created_at: iso(-43),
      request_id: "req_mock_finalize",
    })
  }
  if (inv.status === "PAID") {
    trail.push({
      id: "aud_3",
      action: "invoice.paid",
      cause: "wallet_webhook",
      actor: "service:ctech-wallet",
      before: "OPEN",
      after: "PAID",
      created_at: iso(-28),
      request_id: "req_mock_paid",
    })
  }
  if (inv.status === "UNCOLLECTIBLE") {
    trail.push({
      id: "aud_4",
      action: "invoice.uncollectible",
      cause: "dunning_exhausted",
      actor: "scheduler",
      before: "OPEN",
      after: "UNCOLLECTIBLE",
      created_at: iso(-18),
      request_id: "req_mock_uncollectible",
    })
  }
  return trail
}

export function consoleInvoices(mode: "live" | "test"): ConsoleInvoiceRow[] {
  return mode === "test" ? TEST : LIVE
}

export function consoleInvoiceDetail(inv: ConsoleInvoiceRow): ConsoleInvoiceDetail {
  const notes =
    inv.status === "PAID"
      ? [
          {
            id: "cn_mock_1",
            invoice_id: inv.id,
            amount: 2400,
            currency: "BRL",
            reason: "Emissões cobradas em duplicidade",
            refunded_externally: true,
            external_refund_ref: "ref_mock_1",
            created_by: "user:01JMOCKOPERATOR",
            created_at: iso(-20),
          },
        ]
      : []
  const credited = notes.reduce((sum, note) => sum + note.amount, 0)
  return {
    invoice: inv,
    customer_name: inv.customer_name,
    credit_notes: notes,
    credited,
    fully_credited: inv.total > 0 && credited >= inv.total,
    timeline: timelineFor(inv),
  }
}


// ── Customers, subscriptions and the catalogue ───────────────────────────────
//
// One of each state that changes the screen, and no more: a fixture set that
// mirrors production is a fixture set nobody maintains. What matters here is
// that every branch a console screen has — archived price, metered price,
// subscription ending at period end, anonymized customer — has something to
// render.

const CUSTOMERS: ConsoleCustomer[] = [
  {
    id: "cus_mock_ana",
    name: "Ana Ribeiro",
    email: "ana@exemplo.com.br",
    tax_id_masked: "***.456.789-**",
    external_ref: "dfe:12345",
    anonymized: false,
    livemode: true,
  },
  {
    id: "cus_mock_padaria",
    name: "Padaria do Bairro Ltda",
    email: "contato@padaria.example",
    tax_id_masked: "**.345.678/0001-**",
    anonymized: false,
    livemode: true,
  },
  {
    id: "cus_mock_anon",
    name: "Cliente anonimizado",
    anonymized: true,
    livemode: true,
  },
]

const SUBSCRIPTIONS: ConsoleSubscription[] = [
  {
    id: "sub_mock_ana",
    customer_id: "cus_mock_ana",
    status: "ACTIVE",
    entitled: true,
    recurrence: {interval: "month", count: 1},
    billing_timing: "advance",
    anchor: day(-42),
    current_period: {start: day(-12), end: day(18)},
    cancel_at_period_end: false,
    livemode: true,
  },
  {
    id: "sub_mock_padaria",
    customer_id: "cus_mock_padaria",
    status: "PAST_DUE",
    entitled: false,
    recurrence: {interval: "month", count: 1},
    billing_timing: "arrears",
    anchor: day(-70),
    current_period: {start: day(-10), end: day(20)},
    cancel_at_period_end: true,
    livemode: true,
  },
  {
    id: "sub_mock_novo",
    customer_id: "cus_mock_anon",
    status: "INCOMPLETE",
    entitled: false,
    recurrence: {interval: "month", count: 1},
    billing_timing: "advance",
    anchor: day(-2),
    current_period: {start: day(-2), end: day(28)},
    cancel_at_period_end: false,
    livemode: true,
  },
]

const PRICES: ConsolePrice[] = [
  {
    id: "price_mock_essencial",
    product_id: "prod_mock_essencial",
    type: "fixed",
    currency: "BRL",
    unit_amount: 8900,
    recurrence: {interval: "month", count: 1},
    billing_timing: "advance",
    archived: false,
  },
  {
    id: "price_mock_essencial_antigo",
    product_id: "prod_mock_essencial",
    type: "fixed",
    currency: "BRL",
    unit_amount: 7900,
    recurrence: {interval: "month", count: 1},
    billing_timing: "advance",
    archived: true,
  },
  {
    id: "price_mock_emissoes",
    product_id: "prod_mock_emissoes",
    type: "metered",
    currency: "BRL",
    unit_amount: 20,
    recurrence: {interval: "month", count: 1},
    billing_timing: "arrears",
    archived: false,
  },
]

// Declared before PRODUCTS because a product's inherited policy is the same
// list the settings screen shows.
const DEFAULT_DUNNING_SEED: DunningStep[] = [
  {offset: -3, action: "remind"},
  {offset: 1, action: "remind"},
  {offset: 3, action: "remind"},
  {offset: 7, action: "remind"},
  {offset: 10, action: "escalate"},
  {offset: 30, action: "abandon"},
]

const PRODUCTS: ConsoleProduct[] = [
  {
    id: "prod_mock_essencial",
    name: "Plano Essencial",
    active: true,
    livemode: true,
    dunning: {steps: DEFAULT_DUNNING_SEED, custom: false},
    prices: PRICES.filter(price => price.product_id === "prod_mock_essencial"),
  },
  {
    id: "prod_mock_emissoes",
    name: "Emissões adicionais",
    active: true,
    livemode: true,
    dunning: {
      steps: [
        {offset: -1, action: "remind"},
        {offset: 5, action: "escalate"},
        {offset: 20, action: "abandon"},
      ],
      custom: true,
    },
    prices: PRICES.filter(price => price.product_id === "prod_mock_emissoes"),
  },
]

export function consoleCustomers(mode: "live" | "test"): ConsoleCustomer[] {
  return mode === "test" ? CUSTOMERS.slice(0, 1) : CUSTOMERS
}

export function consoleCustomerDetail(customer: ConsoleCustomer): ConsoleCustomerDetail {
  return {
    customer,
    subscriptions: SUBSCRIPTIONS.filter(sub => sub.customer_id === customer.id),
    timeline: [
      {
        id: "aud_cus_1",
        action: "customer.created",
        cause: "manual",
        actor: "user:01JMOCKOPERATOR",
        after: customer.name,
        created_at: iso(-402),
        request_id: "req_mock_customer",
      },
    ],
  }
}

export function consoleSubscriptions(mode: "live" | "test"): ConsoleSubscription[] {
  return mode === "test" ? SUBSCRIPTIONS.slice(0, 1) : SUBSCRIPTIONS
}

export function consoleSubscriptionDetail(sub: ConsoleSubscription): ConsoleSubscriptionDetail {
  const price = sub.id === "sub_mock_padaria" ? PRICES[2] : PRICES[0]
  return {
    subscription: sub,
    items: [{id: "si_mock_1", price_id: price.id, quantity: 1, price}],
    timeline: [
      {
        id: "aud_sub_1",
        action: "subscription.created",
        cause: "manual",
        actor: "client:ctech-dfe",
        created_at: iso(-402),
        request_id: "req_mock_sub",
      },
    ],
  }
}

export function consoleProducts(): ConsoleProduct[] {
  return PRODUCTS
}

export function consoleProduct(id: string): ConsoleProduct | undefined {
  return PRODUCTS.find(product => product.id === id)
}


// ── Dunning, settings and the overview ───────────────────────────────────────

const DEFAULT_DUNNING = DEFAULT_DUNNING_SEED

// Mutable, so the editor's save is visible without a reload — the half of that
// flow a static fixture cannot show.
let orgDunning: DunningStep[] | null = null

export function consoleDunning(): DunningPolicy {
  return orgDunning
    ? {steps: orgDunning, custom: true}
    : {steps: DEFAULT_DUNNING, custom: false}
}

export function setConsoleDunning(steps: DunningStep[]): DunningPolicy {
  orgDunning = steps.length > 0 ? steps : null
  return consoleDunning()
}

export function consoleSettings(mode: "live" | "test") {
  return {
    organization: MOCK_CONSOLE_SESSION[mode],
    dunning: consoleDunning(),
    numbering: "sequencial por ano, sem lacunas",
    retention: "faturas e notas de crédito permanentes; auditoria por 5 anos",
  }
}

// Derived from the invoice fixtures rather than hardcoded: the overview and the
// list must agree, and two hand-written sets of numbers are two sets that drift.
export function consoleOverview(mode: "live" | "test", year: number, month: number) {
  const invoices = consoleInvoices(mode)
  const today = new Date()
  let received = 0
  let open = 0
  let overdue = 0
  let drafts = 0
  let uncollectible = 0
  let overdueCount = 0

  for (const inv of invoices) {
    switch (inv.status) {
      case "PAID":
        received += inv.amount_paid
        break
      case "DRAFT":
        drafts++
        break
      case "UNCOLLECTIBLE":
        uncollectible++
        break
      case "OPEN":
        if (new Date(inv.due_date) < today) {
          overdue += inv.amount_due
          overdueCount++
        } else {
          open += inv.amount_due
        }
        break
    }
  }

  return {
    year,
    month,
    received,
    open,
    overdue,
    drafts,
    uncollectible,
    overdue_count: overdueCount,
    counted: invoices.length,
    complete: true,
  }
}

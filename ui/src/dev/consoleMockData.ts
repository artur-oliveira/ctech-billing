import type {
  ConsoleCredential,
  ConsoleWebhookEndpoint,
  ApplicationStatus,
  AuditEntry,
  ConsoleCustomer,
  ConsoleCustomerDetail,
  ConsoleInvoice,
  ConsoleInvoiceDetail,
  ConsoleInvoiceRow,
  ConsolePrice,
  ConsoleProduct,
  ConsoleSession,
  ConsoleTenant,
  MerchantApplication,
  MerchantCompany,
  MerchantOrganization,
  ConsoleSubscription,
  ConsoleSubscriptionDetail,
  ConsoleSubscriptionRow,
  DunningPolicy,
  DunningStep,
} from "@/lib/api/consoleTypes";

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
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};

const iso = (offset: number): string => {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return d.toISOString();
};

export const MOCK_MERCHANT_ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
export const MOCK_NONE_ORG = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb";

export const MOCK_CONSOLE_TENANT: Record<"live" | "test", ConsoleTenant> = {
  live: {organization_id: "org_mock_ctech", display_name: "CTech Tecnologia", livemode: true, payout_status: "enabled", can_charge: true},
  test: {organization_id: "org_mock_ctech", display_name: "CTech Tecnologia", livemode: false, payout_status: "enabled", can_charge: true},
};

/** The session per scenario: `sem_cobranca` administers one organization with no tenant;
 *  `pedido_em_analise` / `pedido_recusado` put Padaria's application in review or rejected;
 *  `cobranca_suspensa` suspends CTech's live tenant. CTech plays tenant zero (it reviews). */
export function consoleSession(scenario: string | null): ConsoleSession {
  const padariaStatus = scenario === "pedido_em_analise" ? "test" : scenario === "pedido_recusado" ? "rejected" : "none";
  const none: MerchantOrganization = {
    organization_id: MOCK_NONE_ORG, selector: `org:${MOCK_NONE_ORG}`, display_name: "Padaria Bom Dia", role: "admin",
    merchant_status: padariaStatus,
    ...(scenario === "pedido_em_analise" ? {application_status: "pending" as const} : {}),
    ...(scenario === "pedido_recusado" ? {application_status: "rejected" as const} : {}),
  };
  if (scenario === "sem_cobranca") return {organizations: [none], organizations_unavailable: false};
  return {
    organizations: [
      {
        organization_id: MOCK_MERCHANT_ORG, selector: `org:${MOCK_MERCHANT_ORG}`, display_name: "CTech Tecnologia", role: "owner",
        merchant_status: scenario === "cobranca_suspensa" ? "suspended" : "live", can_review_applications: true,
      },
      none,
    ],
    organizations_unavailable: false,
  };
}

export const MOCK_COMPANIES: MerchantCompany[] = [
  {company_id: "cmp_mock_padaria", legal_name: "Padaria Bom Dia LTDA", trade_name: "Padaria Bom Dia", tax_id: "12345678000190"},
];

export function mockApplication(status: ApplicationStatus): MerchantApplication {
  return {
    id: "apl_mock_padaria", status, organization_id: MOCK_NONE_ORG, organization_name: "Padaria Bom Dia",
    company: {id: "cmp_mock_padaria", legal_name: "Padaria Bom Dia LTDA", tax_id: "12345678000190"},
    description: "Assinaturas semanais de pão e café para escritórios do bairro.",
    website: "https://padaria.example", expected_monthly_volume: "up_to_10k", contact_email: "contato@padaria.example",
    applied_by: "usr_mock", applied_at: "2026-10-09T14:20:00Z",
    ...(status === "rejected" ? {decided_at: "2026-10-10T09:00:00Z", rejection_reason: "O site não abre. Envie de novo com o endereço certo."} : {}),
  };
}

function invoice(over: Partial<ConsoleInvoiceRow> & Pick<ConsoleInvoice, "id" | "status">): ConsoleInvoiceRow {
  const total = over.total ?? 11300;
  return {
    number: 1042,
    customer_id: "cus_mock_ana",
    customer_name: "Ana Ribeiro",
    subscription_id: "sub_mock_ana",
    overdue: false,
    period: {period_start: day(-42), period_end: day(-13)},
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
        period: {period_start: day(-42), period_end: day(-13)},
        quantity: 1,
        unit_amount: 8900,
        amount: 8900,
        proration: false,
      },
      {
        description: "Emissões adicionais (120 documentos)",
        period: {period_start: day(-42), period_end: day(-13)},
        quantity: 120,
        unit_amount: 20,
        amount: 2400,
        proration: false,
      },
    ],
    ...over,
  };
}

const LIVE: ConsoleInvoiceRow[] = [
  invoice({
    id: "in_mock_0044",
    number: 1044,
    status: "OPEN",
    overdue: true,
    due_date: day(-6),
    period: {period_start: day(-36), period_end: day(-7)},
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
        period: {period_start: day(-12), period_end: day(18)},
        quantity: 1,
        unit_amount: 8900,
        amount: 8900,
        proration: false,
      },
    ],
  }),
];

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
        period: {period_start: day(-25), period_end: day(5)},
        quantity: 1,
        unit_amount: 100,
        amount: 100,
        proration: false,
      },
    ],
  }),
];

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
  ];
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
    });
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
    });
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
    });
  }
  return trail;
}

export function consoleInvoices(mode: "live" | "test"): ConsoleInvoiceRow[] {
  return mode === "test" ? TEST : LIVE;
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
      : [];
  const credited = notes.reduce((sum, note) => sum + note.amount, 0);
  return {
    invoice: inv,
    customer_name: inv.customer_name,
    credit_notes: notes,
    credited,
    fully_credited: inv.total > 0 && credited >= inv.total,
    timeline: timelineFor(inv),
  };
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
];

const SUBSCRIPTIONS: ConsoleSubscriptionRow[] = [
  {
    id: "sub_mock_ana",
    customer_id: "cus_mock_ana",
    customer_name: "Ana Ribeiro",
    plan_name: "Plano Essencial",
    status: "ACTIVE",
    entitled: true,
    recurrence: {interval: "month", count: 1},
    billing_timing: "advance",
    anchor: day(-42),
    current_period: {period_start: day(-12), period_end: day(18)},
    cancel_at_period_end: false,
    livemode: true,
  },
  {
    id: "sub_mock_padaria",
    customer_id: "cus_mock_padaria",
    customer_name: "Padaria do Bairro Ltda",
    plan_name: "Emissões adicionais",
    status: "PAST_DUE",
    entitled: false,
    recurrence: {interval: "month", count: 1},
    billing_timing: "arrears",
    anchor: day(-70),
    current_period: {period_start: day(-10), period_end: day(20)},
    cancel_at_period_end: true,
    livemode: true,
  },
  {
    id: "sub_mock_novo",
    customer_id: "cus_mock_anon",
    customer_name: "Cliente anonimizado",
    plan_name: "Plano Essencial",
    status: "INCOMPLETE",
    entitled: false,
    recurrence: {interval: "month", count: 1},
    billing_timing: "advance",
    anchor: day(-2),
    current_period: {period_start: day(-2), period_end: day(28)},
    cancel_at_period_end: false,
    livemode: true,
  },
];

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
];

// Declared before PRODUCTS because a product's inherited policy is the same
// list the settings screen shows.
const DEFAULT_DUNNING_SEED: DunningStep[] = [
  {offset: -3, action: "remind"},
  {offset: 1, action: "remind"},
  {offset: 3, action: "remind"},
  {offset: 7, action: "remind"},
  {offset: 10, action: "escalate"},
  {offset: 30, action: "abandon"},
];

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
];

export function consoleCustomers(mode: "live" | "test"): ConsoleCustomer[] {
  return mode === "test" ? CUSTOMERS.slice(0, 1) : CUSTOMERS;
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
  };
}

export function consoleSubscriptions(mode: "live" | "test"): ConsoleSubscriptionRow[] {
  return mode === "test" ? SUBSCRIPTIONS.slice(0, 1) : SUBSCRIPTIONS;
}

export function consoleSubscriptionDetail(sub: ConsoleSubscription): ConsoleSubscriptionDetail {
  const price = sub.id === "sub_mock_padaria" ? PRICES[2] : PRICES[0];
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
  };
}

export function consoleProducts(): ConsoleProduct[] {
  return PRODUCTS;
}

export function consoleProduct(id: string): ConsoleProduct | undefined {
  return PRODUCTS.find(product => product.id === id);
}


// ── Dunning, settings and the overview ───────────────────────────────────────

const DEFAULT_DUNNING = DEFAULT_DUNNING_SEED;

// Mutable, so the editor's save is visible without a reload — the half of that
// flow a static fixture cannot show.
let orgDunning: DunningStep[] | null = null;

export function consoleDunning(): DunningPolicy {
  return orgDunning
    ? {steps: orgDunning, custom: true}
    : {steps: DEFAULT_DUNNING, custom: false};
}

export function setConsoleDunning(steps: DunningStep[]): DunningPolicy {
  orgDunning = steps.length > 0 ? steps : null;
  return consoleDunning();
}

// Mutable for the same reason the policy is: the editor's save has to be
// visible without a reload.
let issuer = {
  legal_name: "A O CARVALHO TECH LTDA",
  tax_id: "12.345.678/0001-90",
  address: "São Paulo/SP",
  email: "cobranca@aoctech.app",
};

export function consoleIssuer() {
  return issuer;
}

export function setConsoleIssuer(next: typeof issuer) {
  issuer = next;
  return issuer;
}

export function consoleSettings(mode: "live" | "test") {
  return {
    organization: MOCK_CONSOLE_TENANT[mode],
    dunning: consoleDunning(),
    issuer,
    documents_enabled: true,
    numbering: "sequential_per_year_gapless",
    retention: "invoices_credit_notes_permanent_audit_5y",
  };
}

// Derived from the invoice fixtures rather than hardcoded: the overview and the
// list must agree, and two hand-written sets of numbers are two sets that drift.
export function consoleOverview(mode: "live" | "test", year: number, month: number) {
  const invoices = consoleInvoices(mode);
  const today = new Date();
  let received = 0;
  let open = 0;
  let overdue = 0;
  let drafts = 0;
  let uncollectible = 0;
  let overdueCount = 0;

  for (const inv of invoices) {
    switch (inv.status) {
      case "PAID":
        received += inv.amount_paid;
        break;
      case "DRAFT":
        drafts++;
        break;
      case "UNCOLLECTIBLE":
        uncollectible++;
        break;
      case "OPEN":
        if (new Date(inv.due_date) < today) {
          overdue += inv.amount_due;
          overdueCount++;
        } else {
          open += inv.amount_due;
        }
        break;
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
  };
}

export const MOCK_MACHINE_SCOPES = [
  "billing:customers:read", "billing:customers:write", "billing:entitlements:read", "billing:invoices:read",
  "billing:invoices:write", "billing:organization:read", "billing:products:read", "billing:products:write",
  "billing:subscriptions:read", "billing:subscriptions:write", "billing:usage:write",
];
export const MOCK_EVENT_TYPES = [
  "checkout.session.canceled", "checkout.session.completed", "checkout.session.created", "checkout.session.expired",
  "credit_note.created", "invoice.created", "invoice.finalized", "invoice.paid", "invoice.payment_failed",
  "invoice.uncollectible", "invoice.voided", "payment.abandoned", "payment.attempted", "payment.failed",
  "payment.succeeded", "subscription.activated", "subscription.canceled", "subscription.created",
  "subscription.past_due", "subscription.paused", "subscription.recovered", "subscription.renewed",
  "subscription.resumed", "subscription.updated",
];
/** Mutable per page load: the mock is a demo, not a store. */
export const mockCredentials: ConsoleCredential[] = [
  {client_id: "dfe-billing", livemode: true, active: true, provisioned: true, connected_by_me: false, description: "CTech DF-e"},
];
export const mockWebhooks: ConsoleWebhookEndpoint[] = [
  {id: "whe_dfe", url: "https://dfe.internal.aoctech.app/v1/internal/webhooks/billing", events: [], status: "active", failure_run: 0, provisioned: true, owner_key: "dfe"},
];

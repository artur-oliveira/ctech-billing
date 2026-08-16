import type {Invoice, Subscription} from "@/lib/api/types"
import type {MockScenario} from "@/lib/mockConfig"

/**
 * Fixtures are generated relative to today, not frozen.
 *
 * A hardcoded due date stops meaning "vence em 3 dias" the moment three days
 * pass, and the screen it was written to exercise quietly becomes the overdue
 * screen. Everything here is computed from the day the mock runs, so
 * `vence_em_3_dias` says that in March and still says it in December.
 *
 * The `state` and `tone` strings reproduce what `invoiceState` in
 * api/internal/api/v1/portal_dto.go returns for the same inputs. They are not
 * the UI's opinion — the UI has no opinion about state, which is the whole
 * point of the server sending a phrase.
 */

const day = (offset: number): string => {
  const d = new Date()
  d.setDate(d.getDate() + offset)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
}

const thisPeriod = {start: day(-12), end: day(18)}
const lastPeriod = {start: day(-42), end: day(-13)}

const PLAN_LINES = [
  {description: "Plano Essencial · mensal", amount: 8900, proration: false},
  {description: "Emissões adicionais (120 documentos)", amount: 2400, proration: false},
]

function invoice(over: Partial<Invoice> & Pick<Invoice, "id" | "state" | "tone">): Invoice {
  return {
    number: 1042,
    description: "Plano Essencial · mensal e mais 1",
    due_date: day(3),
    total: 11300,
    amount_due: 11300,
    currency: "BRL",
    period: thisPeriod,
    lines: PLAN_LINES,
    payable: true,
    ...over,
  }
}

const PAID_HISTORY: Invoice[] = [
  invoice({
    id: "inv_mock_0041",
    number: 1041,
    state: "Paga",
    tone: "positive",
    due_date: day(-27),
    amount_paid: 11300,
    amount_due: 0,
    payable: false,
    period: lastPeriod,
  }),
  invoice({
    id: "inv_mock_0040",
    number: 1040,
    state: "Paga",
    tone: "positive",
    due_date: day(-57),
    amount_paid: 10900,
    amount_due: 0,
    total: 10900,
    payable: false,
    period: {start: day(-72), end: day(-43)},
    lines: [{description: "Plano Essencial · mensal", amount: 10900, proration: false}],
  }),
]

const ACTIVE_SUB: Subscription = {
  id: "sub_mock_ana",
  description: "Plano Essencial",
  state: "Ativa",
  tone: "positive",
  renews_on: thisPeriod.end,
  amount: 8900,
  metered: false,
  currency: "BRL",
  current_period: thisPeriod,
  cancelable: true,
}

const METERED_SUB: Subscription = {
  id: "sub_mock_uso",
  description: "Emissões adicionais",
  state: "Ativa",
  tone: "positive",
  renews_on: thisPeriod.end,
  metered: true,
  currency: "BRL",
  current_period: thisPeriod,
  cancelable: true,
}

interface Fixture {
  invoices: Invoice[]
  subscriptions: Subscription[]
  /** Seconds after `pagar` before the stream reports the charge settled.
   *  `null` means it never does — the expiry path. */
  settleAfterSeconds: number | null
  /** Seconds the PIX has left when the screen opens it. */
  pixTtlSeconds: number
}

const OPEN_3_DAYS = invoice({
  id: "inv_mock_0042",
  state: "Vence em 3 dias",
  tone: "attention",
})

export const FIXTURES: Record<MockScenario, Fixture> = {
  em_dia: {
    invoices: PAID_HISTORY,
    subscriptions: [ACTIVE_SUB, METERED_SUB],
    settleAfterSeconds: 6,
    pixTtlSeconds: 1800,
  },

  vence_em_3_dias: {
    invoices: [OPEN_3_DAYS, ...PAID_HISTORY],
    subscriptions: [ACTIVE_SUB, METERED_SUB],
    settleAfterSeconds: 6,
    pixTtlSeconds: 1800,
  },

  vencida: {
    invoices: [
      invoice({
        id: "inv_mock_0042",
        state: "Vencida há 4 dias",
        tone: "urgent",
        due_date: day(-4),
      }),
      ...PAID_HISTORY,
    ],
    subscriptions: [
      {...ACTIVE_SUB, state: "Pagamento pendente", tone: "urgent"},
      METERED_SUB,
    ],
    settleAfterSeconds: 6,
    pixTtlSeconds: 1800,
  },

  pagamento_pendente: {
    invoices: [
      invoice({id: "inv_mock_0042", state: "Vence hoje", tone: "urgent", due_date: day(0)}),
      ...PAID_HISTORY,
    ],
    subscriptions: [{...ACTIVE_SUB, state: "Pagamento pendente", tone: "urgent"}],
    settleAfterSeconds: 6,
    pixTtlSeconds: 1800,
  },

  // The charge is already open and has most of its life left.
  pix_aberto: {
    invoices: [OPEN_3_DAYS, ...PAID_HISTORY],
    subscriptions: [ACTIVE_SUB],
    settleAfterSeconds: 8,
    pixTtlSeconds: 1500,
  },

  // Forty seconds of life and nothing ever settles: the expiry screen, which
  // is otherwise a thirty-minute wait to see once.
  pix_expirado: {
    invoices: [OPEN_3_DAYS, ...PAID_HISTORY],
    subscriptions: [ACTIVE_SUB],
    settleAfterSeconds: null,
    pixTtlSeconds: 40,
  },

  paga: {
    invoices: [
      invoice({
        id: "inv_mock_0042",
        state: "Paga",
        tone: "positive",
        amount_paid: 11300,
        amount_due: 0,
        payable: false,
      }),
      ...PAID_HISTORY,
    ],
    subscriptions: [ACTIVE_SUB, METERED_SUB],
    settleAfterSeconds: 6,
    pixTtlSeconds: 1800,
  },

  pendente_de_acordo: {
    invoices: [
      invoice({
        id: "inv_mock_0042",
        state: "Pendente de acordo",
        tone: "attention",
        due_date: day(-38),
        payable: false,
      }),
      ...PAID_HISTORY,
    ],
    subscriptions: [{...ACTIVE_SUB, state: "Pausada", tone: "neutral", renews_on: undefined}],
    settleAfterSeconds: null,
    pixTtlSeconds: 1800,
  },

  sem_assinatura: {
    invoices: [],
    subscriptions: [],
    settleAfterSeconds: null,
    pixTtlSeconds: 1800,
  },

  // Every request fails. The fixture is empty because nothing is ever served.
  erro_de_rede: {
    invoices: [],
    subscriptions: [],
    settleAfterSeconds: null,
    pixTtlSeconds: 1800,
  },

  // Every request answers 503, which the client turns into /maintenance. Also
  // empty: the point is the redirect, and the probe that undoes it.
  manutencao: {
    invoices: [],
    subscriptions: [],
    settleAfterSeconds: null,
    pixTtlSeconds: 1800,
  },
}

/** A copy-and-paste PIX payload of realistic length, so the screen is laid out
 *  against the string people actually receive rather than a short placeholder. */
export const MOCK_PIX_CODE =
  "00020126580014br.gov.bcb.pix0136mock-a1b2c3d4-e5f6-4789-a0b1-c2d3e4f5a6b7520400005303986540" +
  "5113.005802BR5921CTECH TECNOLOGIA LTDA6009SAO PAULO62070503***6304A1B2"

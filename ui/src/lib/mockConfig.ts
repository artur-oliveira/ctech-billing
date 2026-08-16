/**
 * Mock mode is exclusively a local-development facility. The NODE_ENV guard
 * prevents a public environment variable from enabling it in production, and
 * next.config.ts additionally aliases this module away in a production build
 * so the fixtures are not merely disabled but absent.
 */
export const USE_MOCK =
  process.env.NODE_ENV !== "production" && process.env.NEXT_PUBLIC_MOCK_API === "true"

export const MOCK_CUSTOMER = {
  customer_id: "cus_mock_ana",
  name: "Ana Ribeiro",
  email: "ana@exemplo.com.br",
  // Already accepted, so the fixtures below render the screens they exist to
  // render. The gate itself has its own scenario — see `termos_pendentes`.
  terms_accepted: true,
}

/** The same person before they have agreed to the terms addendum. It is a
 *  separate constant rather than a flag because the gate replaces the whole
 *  portal, so it is a state every fixture would otherwise have to opt out of. */
export const MOCK_CUSTOMER_PENDING_TERMS = {...MOCK_CUSTOMER, terms_accepted: false}

/**
 * The states the portal has to render correctly, as named fixtures.
 *
 * They exist because most of them cannot be produced on demand against a real
 * backend: an overdue invoice needs a due date in the past, an expired PIX
 * needs thirty minutes of waiting, and "pendente de acordo" needs a dunning
 * run that has not been built. A screen whose hardest states can only be seen
 * in production is a screen whose hardest states are never designed.
 */
export type MockScenario =
  | "sem_assinatura"
  | "sem_conta"
  | "em_dia"
  | "vence_em_3_dias"
  | "vencida"
  | "pagamento_pendente"
  | "pix_aberto"
  | "pix_expirado"
  | "paga"
  | "pendente_de_acordo"
  | "termos_pendentes"
  | "erro_de_rede"
  | "manutencao"

export const MOCK_SCENARIOS: { id: MockScenario; label: string }[] = [
  {id: "em_dia", label: "Em dia"},
  {id: "vence_em_3_dias", label: "Vence em 3 dias"},
  {id: "vencida", label: "Vencida"},
  {id: "pagamento_pendente", label: "Assinatura com pagamento pendente"},
  {id: "pix_aberto", label: "PIX aberto"},
  {id: "pix_expirado", label: "PIX expirado"},
  {id: "paga", label: "Fatura paga"},
  {id: "pendente_de_acordo", label: "Pendente de acordo"},
  {id: "sem_assinatura", label: "Cliente sem assinatura"},
  {id: "sem_conta", label: "Sem conta de cobrança (403)"},
  {id: "termos_pendentes", label: "Termos pendentes"},
  {id: "erro_de_rede", label: "Erro de rede"},
  {id: "manutencao", label: "Manutenção (503)"},
]

export const DEFAULT_SCENARIO: MockScenario = "vence_em_3_dias"

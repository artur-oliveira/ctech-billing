import type {Account} from "@/lib/api/financeTypes"

import {t} from "@/lib/i18n"

/** The Portuguese names the server seeds default accounts with, by system_key. A stored name that still equals its default was never renamed, so it is shown translated. */
const SEEDED_NAMES: Record<string, string> = {
  payables: "Contas a pagar",
  receivables: "Contas a receber",
  opening_balance: "Saldos iniciais",
  interest_and_fines: "Juros e multas",
  discounts_obtained: "Descontos obtidos",
  salary: "Salário",
  investment_income: "Rendimentos",
  other_income: "Outras receitas",
  housing: "Moradia",
  food: "Alimentação",
  transport: "Transporte",
  health: "Saúde",
  education: "Educação",
  leisure: "Lazer",
  subscriptions_services: "Assinaturas e serviços",
  taxes_and_fees: "Impostos e taxas",
  other_expenses: "Outras despesas",
  sales: "Vendas",
  services_revenue: "Prestação de serviços",
  sales_taxes: "Impostos sobre vendas",
  cost_of_sales: "Custo das vendas e serviços",
  payroll_and_charges: "Salários e encargos",
  rent: "Aluguel",
  software_subscriptions: "Software e assinaturas",
  marketing: "Marketing",
  administrative_expenses: "Despesas administrativas",
  bank_fees: "Tarifas bancárias",
}

const norm = (s: string): string => s.trim().toLowerCase()

type Named = Pick<Account, "name"> & {system_key?: string}

/** The display name of an account: translated for an untouched default, the stored name otherwise (renamed defaults and the person's own accounts). */
export function accountName(a: Named): string {
  const key = a.system_key
  if (key && SEEDED_NAMES[key] && norm(a.name) === norm(SEEDED_NAMES[key])) return t(`finance.systemAccounts.${key}`)
  return a.name
}

/** Sort comparator by display name in the current locale. */
export const byAccountName = (locale: string) => (a: Named, b: Named): number => accountName(a).localeCompare(accountName(b), locale)

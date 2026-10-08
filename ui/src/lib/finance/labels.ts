import type {AccountClass, Bucket, Direction, DREGroup} from "@/lib/api/financeTypes"

/** pt-BR for every enum the finance API sends. The screen never shows the raw value. */
export const BUCKET_LABEL: Record<Bucket, string> = {
  overdue: "Vencida",
  today: "Vence hoje",
  upcoming: "A vencer",
}

export const DIRECTION_LABEL: Record<Direction, string> = {
  payable: "A pagar",
  receivable: "A receber",
}

export const CLASS_LABEL: Record<AccountClass, string> = {
  asset: "Conta",
  liability: "Passivo",
  income: "Receita",
  expense: "Despesa",
  equity: "Patrimônio",
}

export const DRE_GROUP_LABEL: Record<DREGroup, string> = {
  gross_revenue: "Receita bruta",
  deductions: "Deduções",
  costs: "Custos",
  operating_expenses: "Despesas operacionais",
  financial_result: "Resultado financeiro",
  other: "Outros",
}

/** Sunday first, as time.Weekday and Date#getDay number them. */
export const WEEKDAY_LABEL = ["domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado"] as const

const GROUPS: DREGroup[] = ["gross_revenue", "deductions", "costs", "operating_expenses", "financial_result", "other"]

/** The DRE groups an account class may take — mirrors DREGroup.AllowsClass on the server. */
export function groupsForClass(c: AccountClass): DREGroup[] {
  return GROUPS.filter(g => {
    switch (g) {
      case "gross_revenue":
        return c === "income"
      case "deductions":
      case "costs":
      case "operating_expenses":
        return c === "expense"
      default:
        return c === "income" || c === "expense"
    }
  })
}

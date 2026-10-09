import type {AccountClass, Bucket, Direction, DREGroup} from "@/lib/api/financeTypes"

import {t} from "@/lib/i18n"

/** Every enum the finance API sends has a label; the screen never shows the raw value. Resolved at call time so a language switch applies. */
export const bucketLabel = (b: Bucket): string => t(`finance.labels.bucket.${b}`)
export const directionLabel = (d: Direction): string => t(`finance.labels.direction.${d}`)
export const classLabel = (c: AccountClass): string => t(`finance.labels.class.${c}`)
export const dreGroupLabel = (g: DREGroup): string => t(`finance.labels.dreGroup.${g}`)

/** Sunday first, as time.Weekday and Date#getDay number them. Undefined outside 0..6. */
export const weekdayLabel = (i: number): string | undefined => (Number.isInteger(i) && i >= 0 && i <= 6 ? t(`finance.labels.weekday.${i}`) : undefined)

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

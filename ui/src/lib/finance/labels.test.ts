import {describe, expect, it} from "vitest"

import type {AccountClass, Bucket, Direction, DREGroup} from "@/lib/api/financeTypes"
import {
  bucketLabel, classLabel, directionLabel, dreGroupLabel, groupsForClass, weekdayLabel,
} from "@/lib/finance/labels"

// "The consumer never reads an internal name" applies to the console's finance
// section too: every enum value the API can send has a pt-BR label.
describe("labels", () => {
  it("cover every value the API sends", () => {
    for (const k of ["overdue", "today", "upcoming"]) expect(bucketLabel(k as Bucket)).not.toContain("finance.")
    for (const k of ["asset", "liability", "income", "expense", "equity"]) expect(classLabel(k as AccountClass)).not.toContain("finance.")
    for (const k of ["gross_revenue", "deductions", "costs", "operating_expenses", "financial_result", "other"]) {
      expect(dreGroupLabel(k as DREGroup)).not.toContain("finance.")
    }
    for (const k of ["payable", "receivable"]) expect(directionLabel(k as Direction)).not.toContain("finance.")
    for (let i = 0; i < 7; i++) expect(weekdayLabel(i)).not.toContain("finance.")
    expect(weekdayLabel(7)).toBeUndefined()
  })

  it("offer a class only the DRE groups the server accepts for it", () => {
    // Mirrors DREGroup.AllowsClass in api/internal/domain/finance/ledger.go.
    expect(groupsForClass("income")).toEqual(["gross_revenue", "financial_result", "other"])
    expect(groupsForClass("expense")).toEqual(["deductions", "costs", "operating_expenses", "financial_result", "other"])
    expect(groupsForClass("asset")).toEqual([])
  })
})

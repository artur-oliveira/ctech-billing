import {describe, expect, it} from "vitest"

import {
  BUCKET_LABEL, CLASS_LABEL, DIRECTION_LABEL, DRE_GROUP_LABEL, groupsForClass, WEEKDAY_LABEL,
} from "@/lib/finance/labels"

// "The consumer never reads an internal name" applies to the console's finance
// section too: every enum value the API can send has a pt-BR label.
describe("labels", () => {
  it("cover every value the API sends", () => {
    for (const k of ["overdue", "today", "upcoming"]) expect(BUCKET_LABEL[k as keyof typeof BUCKET_LABEL]).toBeTruthy()
    for (const k of ["asset", "liability", "income", "expense", "equity"]) expect(CLASS_LABEL[k as keyof typeof CLASS_LABEL]).toBeTruthy()
    for (const k of ["gross_revenue", "deductions", "costs", "operating_expenses", "financial_result", "other"]) {
      expect(DRE_GROUP_LABEL[k as keyof typeof DRE_GROUP_LABEL]).toBeTruthy()
    }
    for (const k of ["payable", "receivable"]) expect(DIRECTION_LABEL[k as keyof typeof DIRECTION_LABEL]).toBeTruthy()
    expect(WEEKDAY_LABEL).toHaveLength(7)
  })

  it("offer a class only the DRE groups the server accepts for it", () => {
    // Mirrors DREGroup.AllowsClass in api/internal/domain/finance/ledger.go.
    expect(groupsForClass("income")).toEqual(["gross_revenue", "financial_result", "other"])
    expect(groupsForClass("expense")).toEqual(["deductions", "costs", "operating_expenses", "financial_result", "other"])
    expect(groupsForClass("asset")).toEqual([])
  })
})

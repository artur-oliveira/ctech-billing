import {describe, expect, it} from "vitest"

import {
  defaultAnchor, describeModel, type EditorModel, fromExpression, toExpression, validate,
} from "@/lib/finance/expression"

const none: EditorModel["exceptions"] = {months: [], dates: []}
const m = (pattern: EditorModel["pattern"], exceptions = none): EditorModel => ({pattern, exceptions})

describe("toExpression", () => {
  it("emits the stored form for each kind", () => {
    expect<unknown>(toExpression(m({kind: "day_of_month", day: 10}))).toEqual({kind: "day_of_month", day: 10})
    expect<unknown>(toExpression(m({kind: "workday_of_month", n: -1}))).toEqual({kind: "workday_of_month", n: -1})
    expect<unknown>(toExpression(m({kind: "nth_weekday_of_month", weekday: 1, n: 2}))).toEqual({kind: "nth_weekday_of_month", weekday: 1, n: 2})
    expect<unknown>(toExpression(m({kind: "yearly", month: 3, day: 15}))).toEqual({kind: "yearly", month: 3, day: 15})
  })

  it("keeps Sunday as weekday 0 (a falsy 0 dropped would break every Sunday)", () => {
    expect<unknown>(toExpression(m({kind: "weekly", weekday: 0, every: 2, anchor: "2026-01-04"})))
      .toEqual({kind: "weekly", weekday: 0, every: 2, anchor: "2026-01-04"})
  })

  it("wraps exceptions in a difference, months first, then dates", () => {
    expect<unknown>(toExpression(m({kind: "day_of_month", day: 10}, {months: [12], dates: []}))).toEqual({
      kind: "difference", include: {kind: "day_of_month", day: 10}, exclude: {kind: "months_of_year", months: [12]},
    })
    expect<unknown>(toExpression(m({kind: "day_of_month", day: 10}, {months: [12], dates: ["2026-03-10"]}))).toEqual({
      kind: "difference",
      include: {kind: "difference", include: {kind: "day_of_month", day: 10}, exclude: {kind: "months_of_year", months: [12]}},
      exclude: {kind: "dates", dates: ["2026-03-10"]},
    })
  })
})

describe("validate", () => {
  const fields = (model: EditorModel) => validate(model).map(e => e.field)
  it("accepts the edges the server accepts", () => {
    for (const p of [
      {kind: "day_of_month", day: 1}, {kind: "day_of_month", day: 31},
      {kind: "workday_of_month", n: 1}, {kind: "workday_of_month", n: 23}, {kind: "workday_of_month", n: -1},
      {kind: "nth_weekday_of_month", weekday: 6, n: 4}, {kind: "nth_weekday_of_month", weekday: 0, n: -1},
      {kind: "weekly", weekday: 5, every: 52, anchor: "2026-01-02"},
      {kind: "yearly", month: 2, day: 29},
    ] as EditorModel["pattern"][]) {
      expect(validate(m(p))).toEqual([])
    }
  })
  it("refuses what the server refuses", () => {
    expect(fields(m({kind: "day_of_month", day: 0}))).toContain("day")
    expect(fields(m({kind: "day_of_month", day: 32}))).toContain("day")
    expect(fields(m({kind: "workday_of_month", n: 0}))).toContain("n")
    expect(fields(m({kind: "workday_of_month", n: 24}))).toContain("n")
    expect(fields(m({kind: "nth_weekday_of_month", weekday: 1, n: 5}))).toContain("n")
    expect(fields(m({kind: "weekly", weekday: 5, every: 0, anchor: "2026-01-02"}))).toContain("every")
    expect(fields(m({kind: "weekly", weekday: 5, every: 53, anchor: "2026-01-02"}))).toContain("every")
    expect(fields(m({kind: "weekly", weekday: 5, every: 1, anchor: "2026-01-03"}))).toContain("anchor") // a Saturday
    expect(fields(m({kind: "yearly", month: 2, day: 30}))).toContain("day")
    expect(fields(m({kind: "yearly", month: 4, day: 31}))).toContain("day")
  })
  it("refuses exceptions that leave nothing or are too many", () => {
    expect(fields(m({kind: "day_of_month", day: 10}, {months: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12], dates: []}))).toContain("months")
    const many = Array.from({length: 367}, (_, i) => `2026-01-${String((i % 28) + 1).padStart(2, "0")}`)
    expect(fields(m({kind: "day_of_month", day: 10}, {months: [], dates: many}))).toContain("dates")
  })
})

describe("describeModel", () => {
  it("says the rule in plain Portuguese", () => {
    expect(describeModel(m({kind: "day_of_month", day: 10}))).toBe("Todo dia 10")
    expect(describeModel(m({kind: "workday_of_month", n: 5}))).toBe("5º dia útil do mês")
    expect(describeModel(m({kind: "workday_of_month", n: -1}))).toBe("Último dia útil do mês")
    expect(describeModel(m({kind: "nth_weekday_of_month", weekday: 1, n: 2}))).toBe("2ª segunda-feira do mês")
    expect(describeModel(m({kind: "nth_weekday_of_month", weekday: 5, n: -1}))).toBe("Última sexta-feira do mês")
    expect(describeModel(m({kind: "weekly", weekday: 5, every: 1, anchor: "2026-01-02"}))).toBe("Toda sexta-feira")
    expect(describeModel(m({kind: "weekly", weekday: 5, every: 2, anchor: "2026-01-02"}))).toBe("A cada 2 semanas, na sexta-feira")
    expect(describeModel(m({kind: "yearly", month: 3, day: 15}))).toBe("Todo ano em 15 de março")
    expect(describeModel(m({kind: "day_of_month", day: 10}, {months: [12], dates: []}))).toBe("Todo dia 10; exceto em dezembro")
    expect(describeModel(m({kind: "day_of_month", day: 10}, {months: [1, 12], dates: ["2026-03-10"]})))
      .toBe("Todo dia 10; exceto em janeiro e dezembro, em 1 data")
  })
  it("uses no em dash", () => {
    expect(describeModel(m({kind: "day_of_month", day: 10}, {months: [12], dates: ["2026-03-10", "2026-04-10"]}))).not.toContain("—")
  })
})

describe("fromExpression", () => {
  it("round-trips every model it can produce", () => {
    for (const model of [
      m({kind: "day_of_month", day: 31}),
      m({kind: "weekly", weekday: 0, every: 3, anchor: "2026-01-04"}),
      m({kind: "yearly", month: 2, day: 29}, {months: [12], dates: []}),
      m({kind: "workday_of_month", n: 5}, {months: [1, 12], dates: ["2026-03-06"]}),
      m({kind: "nth_weekday_of_month", weekday: 1, n: -1}, {months: [], dates: ["2026-05-25"]}),
    ]) {
      expect(fromExpression(toExpression(model))).toEqual(model)
    }
  })
})

describe("defaultAnchor", () => {
  it("is the first day on or after start that falls on the weekday", () => {
    expect(defaultAnchor(5, "2026-03-02")).toBe("2026-03-06") // Monday -> Friday
    expect(defaultAnchor(1, "2026-03-02")).toBe("2026-03-02") // already a Monday
    expect(defaultAnchor(0, "2026-03-07")).toBe("2026-03-08") // Saturday -> Sunday
  })
})

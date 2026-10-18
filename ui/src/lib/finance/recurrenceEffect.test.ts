import {describe, expect, it} from "vitest";

import type {ExpressionJSON} from "@/lib/api/financeTypes";
import {isMonthly, monthlyEffect, occurrencesPerMonth} from "@/lib/finance/recurrenceEffect";

describe("recurrenceEffect", () => {
  it("counts monthly rules once", () => {
    for (const e of [{kind: "day_of_month", day: 10}, {kind: "workday_of_month", n: 5}, {kind: "nth_weekday_of_month", weekday: 1, n: 1}] as ExpressionJSON[]) {
      expect(occurrencesPerMonth(e)).toBe(1);
      expect(isMonthly(e)).toBe(true);
    }
  });
  it("turns a weekly amount into about a month's worth", () => {
    const weekly: ExpressionJSON = {kind: "weekly", weekday: 6, every: 1, anchor: "2026-01-03"};
    expect(monthlyEffect({amount: 12_000, expression: weekly})).toBe(52_179);
    expect(monthlyEffect({amount: 12_000, expression: {...weekly, every: 2}})).toBe(26_089);
    expect(isMonthly(weekly)).toBe(false);
  });
  it("spreads a yearly amount over twelve months", () => {
    expect(monthlyEffect({amount: 120_000, expression: {kind: "yearly", month: 3, day: 1}})).toBe(10_000);
  });
  it("thins a rule that skips whole months, and ignores single dates", () => {
    const day: ExpressionJSON = {kind: "day_of_month", day: 10};
    const skipping: ExpressionJSON = {kind: "difference", include: day, exclude: {kind: "months_of_year", months: [1, 2, 3]}};
    expect(monthlyEffect({amount: 1_200, expression: skipping})).toBe(900);
    expect(monthlyEffect({amount: 1_200, expression: {kind: "difference", include: day, exclude: {kind: "dates", dates: ["2026-05-10"]}}})).toBe(1_200);
  });
  it("is null for a shape it does not know", () => {
    expect(monthlyEffect({amount: 100, expression: {kind: "dates", dates: ["2026-05-10"]}})).toBeNull();
  });
});

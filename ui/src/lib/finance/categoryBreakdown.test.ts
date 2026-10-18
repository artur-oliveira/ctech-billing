import {describe, expect, it} from "vitest";

import type {CategoryAmounts, CategoryMonth} from "@/lib/api/financeTypes";

import {booked, isEmptyMonth, netOf, share, sidesOf} from "./categoryBreakdown";

const row = (category_id: string, realised: number, scheduled: number, forecast: number): CategoryAmounts =>
  ({category_id, realised, scheduled, forecast, total: realised + scheduled + forecast});
const side = (rows: CategoryAmounts[]) => ({
  totals: {realised: rows.reduce((a, r) => a + r.realised, 0), scheduled: rows.reduce((a, r) => a + r.scheduled, 0), forecast: rows.reduce((a, r) => a + r.forecast, 0), total: rows.reduce((a, r) => a + r.total, 0)},
  categories: rows,
});
const month: CategoryMonth = {month: "2026-10", income: side([row("salary", 500000, 0, 0)]), expense: side([row("rent", 0, 150000, 0), row("food", 30000, 0, 20000)])};

describe("category breakdown helpers", () => {
  it("reads income before expense", () => {
    expect(sidesOf(month).map(s => s.key)).toEqual(["income", "expense"]);
  });
  it("is empty only when neither side has a row", () => {
    expect(isEmptyMonth(month)).toBe(false);
    expect(isEmptyMonth({month: "2026-10", income: side([]), expense: side([])})).toBe(true);
  });
  it("splits booked from forecast", () => {
    expect(booked(month.expense.categories[1])).toBe(30000);
  });
  it("shares are 0..1 and never negative or divide by zero", () => {
    expect(share(25, 100)).toBe(0.25);
    expect(share(-5, 100)).toBe(0);
    expect(share(5, 0)).toBe(0);
    expect(share(500, 100)).toBe(1);
  });
  it("nets income against expense in every column", () => {
    expect(netOf(month)).toEqual({realised: 470000, scheduled: -150000, forecast: -20000, total: 300000});
  });
});

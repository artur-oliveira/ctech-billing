import {describe, expect, it} from "vitest";

import {aggregate, automaticBudget, HORIZONS, maxBars, project} from "@/lib/finance/projection";

describe("project", () => {
  it("accumulates each month's result onto today's balance", () => {
    const [a, b] = project(1000, [
      {month: "2026-03", receivable: 500, payable: 200, virtual: -100},
      {month: "2026-04", receivable: 0, payable: 50, virtual: 0},
    ]);
    expect(a.balance).toBe(1200);
    expect(b.balance).toBe(1150);
    expect(a.recOut).toBe(100);
    expect(a.outflow).toBe(300);
  });
});

describe("project with cards", () => {
  const month = {month: "2026-03", receivable: 0, payable: 1000, virtual: -200, card_payable: 700, virtual_card_payable: 150};

  it("counts the card estimate in the outflow, the result and the balance, and keeps the card part apart", () => {
    const [a] = project(5000, [{...month, card_estimate: 400}]);
    expect(a).toMatchObject({card: 850, estimate: 400, outflow: 1000 + 200 + 400, result: -1000 - 200 - 400, balance: 5000 - 1600});
  });

  it("reads an older API (no card fields) as no card spending and no estimate", () => {
    const [a] = project(5000, [{month: "2026-03", receivable: 0, payable: 1000, virtual: 0}]);
    expect(a).toMatchObject({card: 0, estimate: 0, outflow: 1000, balance: 4000});
  });

  it("sums the card parts when months fold into a period", () => {
    const bars = aggregate(project(0, [{...month, card_estimate: 400}, {...month, month: "2026-04", card_estimate: 100}]), 1);
    expect(bars[0]).toMatchObject({card: 1700, estimate: 500, outflow: 2000 + 400 + 500 - 0});
  });
});

describe("project with a budget of the person's own", () => {
  const by = (unplanned: number, baseline = 40000) => [{card_id: "a", posted: 0, expected: 0, unplanned, estimate: 0, baseline}, {card_id: "b", posted: 0, expected: 0, unplanned: 0, estimate: 0, baseline: 5000}];
  const m = (month: string, unplanned: number) => ({month, receivable: 0, payable: 0, virtual: 0, card_estimate: 777, by_card: by(unplanned)});

  it("replaces the automatic estimate: the budget less what the statements already hold of it, never below zero", () => {
    const [a, b, c] = project(0, [m("2026-03", 15000), m("2026-04", 0), m("2026-05", 90000)], 60000);
    expect([a.estimate, b.estimate, c.estimate]).toEqual([45000, 60000, 0]);
    expect(c.balance).toBe(-(45000 + 60000));
  });

  it("zero is a budget, not 'unset'", () => {
    expect(project(0, [m("2026-03", 0)], 0)[0].estimate).toBe(0);
  });

  it("keeps the automatic estimate without a budget, and on an API that sends no cards", () => {
    expect(project(0, [m("2026-03", 0)], null)[0].estimate).toBe(777);
    expect(project(0, [{month: "2026-03", receivable: 0, payable: 0, virtual: 0, card_estimate: 777}], 60000)[0].estimate).toBe(777);
  });

  it("reads the automatic budget as the cards' usual amounts summed", () => {
    expect(automaticBudget([m("2026-03", 0)])).toBe(45000);
    expect(automaticBudget([{month: "2026-03", receivable: 0, payable: 0, virtual: 0}])).toBeNull();
  });

  it("merges each card's money across the months a period folds", () => {
    const card = (posted: number) => ({card_id: "a", posted, expected: 10, unplanned: 5, estimate: 7, baseline: 99});
    const bars = aggregate(project(0, [
      {month: "2026-03", receivable: 0, payable: 0, virtual: 0, by_card: [card(100)]},
      {month: "2026-04", receivable: 0, payable: 0, virtual: 0, by_card: [card(200)]},
    ]), 1);
    expect(bars[0].cards).toEqual([{card_id: "a", posted: 300, expected: 20, unplanned: 10, estimate: 14, baseline: 99}]);
  });
});

describe("aggregate", () => {
  const raw = (n: number) => Array.from({length: n}, (_, i) => ({month: `${2026 + Math.floor(i / 12)}-${String((i % 12) + 1).padStart(2, "0")}`, receivable: 100, payable: 40, virtual: 10}));

  it("keeps every month when they fit", () => {
    expect(aggregate(project(1000, raw(6)), 12)).toHaveLength(6);
    expect(aggregate(project(1000, raw(6)), 6).every(p => p.count === 1)).toBe(true);
  });

  it("caps the bars: 12 months on a phone are six bi-months, 60 are five years", () => {
    expect(aggregate(project(1000, raw(12)), maxBars(true)).map(p => p.count)).toEqual([2, 2, 2, 2, 2, 2]);
    expect(aggregate(project(0, raw(60)), maxBars(true))).toHaveLength(5);
    expect(aggregate(project(0, raw(60)), maxBars(false))).toHaveLength(10);
    for (const h of HORIZONS) {
      expect(aggregate(project(0, raw(h)), maxBars(true)).length).toBeLessThanOrEqual(6);
      expect(aggregate(project(0, raw(h)), maxBars(false)).length).toBeLessThanOrEqual(12);
    }
  });

  it("sums the flows and ends on the last month's balance, so totals are preserved", () => {
    const all = project(1000, raw(12)), bars = aggregate(all, 4);
    expect(bars[0]).toMatchObject({month: "2026-01", last: "2026-03", count: 3, inflow: 330, outflow: 120, result: 210, balance: all[2].balance});
    expect(bars[bars.length - 1].balance).toBe(all[11].balance);
    expect(bars.reduce((s, b) => s + b.result, 0)).toBe(all.reduce((s, m) => s + m.result, 0));
  });
});

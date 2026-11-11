import {describe, expect, it} from "vitest";

import {aggregate, HORIZONS, maxBars, project} from "@/lib/finance/projection";

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

  it("keeps the card part apart and guesses nothing: no figure from the person, no extra outflow", () => {
    const [a] = project(5000, [month]);
    expect(a).toMatchObject({card: 850, estimate: 0, outflow: 1000 + 200, result: -1000 - 200, balance: 5000 - 1200});
  });

  it("reads an older API (no card fields) as no card spending", () => {
    const [a] = project(5000, [{month: "2026-03", receivable: 0, payable: 1000, virtual: 0}]);
    expect(a).toMatchObject({card: 0, estimate: 0, outflow: 1000, balance: 4000});
  });
});

describe("project with a forecast of the person's own", () => {
  const by = (unplanned: number) => [{card_id: "a", posted: 0, expected: 0, unplanned}, {card_id: "b", posted: 0, expected: 0, unplanned: 0}];
  const m = (month: string, unplanned: number) => ({month, receivable: 0, payable: 0, virtual: 0, by_card: by(unplanned)});

  it("counts the figure less what the statements already hold of it, never below zero, in the outflow, result and balance", () => {
    const [a, b, c] = project(0, [m("2026-03", 15000), m("2026-04", 0), m("2026-05", 90000)], 60000);
    expect([a.estimate, b.estimate, c.estimate]).toEqual([45000, 60000, 0]);
    expect(a.outflow).toBe(45000);
    expect(c.balance).toBe(-(45000 + 60000));
  });

  it("zero is a figure, and without one nothing is added", () => {
    expect(project(0, [m("2026-03", 0)], 0)[0].estimate).toBe(0);
    expect(project(0, [m("2026-03", 0)], null)[0].estimate).toBe(0);
  });

  it("is ignored on an API that sends no cards", () => {
    expect(project(0, [{month: "2026-03", receivable: 0, payable: 0, virtual: 0}], 60000)[0].estimate).toBe(0);
  });

  it("merges each card's money across the months a period folds", () => {
    const card = (posted: number) => ({card_id: "a", posted, expected: 10, unplanned: 5});
    const bars = aggregate(project(0, [
      {month: "2026-03", receivable: 0, payable: 0, virtual: 0, by_card: [card(100)]},
      {month: "2026-04", receivable: 0, payable: 0, virtual: 0, by_card: [card(200)]},
    ]), 1);
    expect(bars[0].cards).toEqual([{card_id: "a", posted: 300, expected: 20, unplanned: 10}]);
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

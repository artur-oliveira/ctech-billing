import {describe, expect, it} from "vitest";

import type {StatementSummary} from "@/lib/api/financeTypes";
import {edges, flatten, initialParam, keepVisible, nextParam, previousParam, shiftMonth, stepWindow, type TimelinePage, windowAt} from "@/lib/finance/statementTimeline";

const s = (month: string): StatementSummary => ({card_id: "c", month, status: "future", closing_date: `${month}-03`, due_date: `${month}-10`, total: 0, items_count: 0});
const page = (direction: "forward" | "backward", months: string[], has_more: boolean, from = months[0]): TimelinePage =>
  ({data: months.map(s), has_more, param: {from, direction, count: months.length}});

describe("statement timeline paging", () => {
  it("shifts months across years", () => {
    expect(shiftMonth("2026-01", -1)).toBe("2025-12");
    expect(shiftMonth("2026-11", 3)).toBe("2027-02");
  });

  it("opens with one request for 3 before, the open one and 3 after", () => {
    expect(initialParam("2026-03")).toEqual({from: "2025-12", direction: "forward", count: 7});
  });

  it("asks beyond the loaded edge in each direction, and stops when there is no more", () => {
    const first = page("forward", ["2025-12", "2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06"], true);
    expect(nextParam(first)).toEqual({from: "2026-07", direction: "forward", count: 3});
    expect(previousParam(first)).toEqual({from: "2025-11", direction: "backward", count: 3});
    const older = page("backward", ["2025-11", "2025-10", "2025-09"], true, "2025-11");
    expect(previousParam(older)).toEqual({from: "2025-08", direction: "backward", count: 3});
    expect(previousParam(page("backward", ["2025-11"], false))).toBeUndefined();
    expect(previousParam(page("backward", [], false))).toBeUndefined();
    expect(nextParam(page("forward", ["2027-03"], false))).toBeUndefined();
  });

  it("flattens pages in month order without duplicates", () => {
    const pages = [page("backward", ["2025-11", "2025-10"], true), page("forward", ["2025-12", "2026-01"], true), page("forward", ["2026-01", "2026-02"], false)];
    expect(flatten(pages).map(x => x.month)).toEqual(["2025-10", "2025-11", "2025-12", "2026-01", "2026-02"]);
  });

  it("windows, steps a page at a time and keeps the selection visible", () => {
    const months = ["2025-12", "2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09"];
    expect(windowAt(months, "2025-12", 7)).toEqual({from: 0, to: 7});
    expect(windowAt(months, "2025-01", 7)).toEqual({from: 0, to: 7}); // a start before the loaded months clamps
    expect(stepWindow(months, "2025-12", 7, 1)).toBe("2026-03");
    expect(stepWindow(months, "2026-03", 7, 1)).toBe("2026-03"); // already at the last full window
    expect(stepWindow(months, "2026-03", 7, -1)).toBe("2025-12");
    expect(keepVisible(months, "2025-12", 7, "2026-07")).toBe("2026-01");
    expect(keepVisible(months, "2026-03", 7, "2026-01")).toBe("2026-01");
    expect(edges(months, "2025-12", 7)).toEqual({before: 0, after: 3});
    expect(edges(months, "2026-03", 7)).toEqual({before: 3, after: 0});
  });
});

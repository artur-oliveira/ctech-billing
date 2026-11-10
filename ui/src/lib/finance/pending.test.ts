import {describe, expect, it} from "vitest";

import type {Bill, PendingOccurrence} from "@/lib/api/financeTypes";
import {pendingToday} from "@/lib/finance/pending";

const bill = (o: Partial<Bill>): Bill => ({id: "x", direction: "payable", amount: 1000, account_id: "cc", category_id: "c", competence_date: "2026-03-01",
  due_date: "2026-03-10", status: "forecast", origin: "manual", auto_settle: false, ...o});
const occ = (o: Partial<PendingOccurrence>): PendingOccurrence => ({recurrence_id: "r", nominal: "2026-03-05", due: "2026-03-05", direction: "payable", expected_amount: 500, category_id: "c", ...o});

describe("pendingToday", () => {
  it("keeps overdue and due-today bills and due occurrences, oldest first; drops the rest", () => {
    const out = pendingToday(
      [bill({id: "up", bucket: "upcoming"}), bill({id: "today", bucket: "today", due_date: "2026-03-10"}), bill({id: "late", bucket: "overdue", due_date: "2026-03-01"}), bill({id: "paid", bucket: "overdue", status: "paid"})],
      [occ({recurrence_id: "due"}), occ({recurrence_id: "future", due: "2026-03-20"})],
      "2026-03-10");
    expect(out.map(i => i.key)).toEqual(["b-late", "o-due-2026-03-05", "b-today"]);
  });

  it("is empty when nothing is due", () => {
    expect(pendingToday([bill({bucket: "upcoming"})], [], "2026-03-10")).toEqual([]);
  });
});

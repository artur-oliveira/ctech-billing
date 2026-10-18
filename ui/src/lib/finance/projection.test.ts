import {describe, expect, it} from "vitest";

import {project} from "@/lib/finance/projection";

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

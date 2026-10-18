import {describe, expect, it} from "vitest";

import {daysBetween, dueLabel} from "./today";

describe("dueLabel", () => {
  it("counts civil days, across month ends", () => {
    expect(daysBetween("2026-02-27", "2026-03-02")).toBe(3);
    expect(daysBetween("2026-03-10", "2026-03-05")).toBe(-5);
  });
  it("says it the way a person does", () => {
    expect(dueLabel("2026-03-05", "2026-03-10")).toBe("Venceu há 5 dias");
    expect(dueLabel("2026-03-09", "2026-03-10")).toBe("Venceu há 1 dia");
    expect(dueLabel("2026-03-10", "2026-03-10")).toBe("Vence hoje");
    expect(dueLabel("2026-03-11", "2026-03-10")).toBe("Vence amanhã");
    expect(dueLabel("2026-03-13", "2026-03-10")).toBe("Vence em 3 dias");
  });
  it("leaves far dates to the date itself", () => {
    expect(dueLabel("2026-06-01", "2026-03-10")).toBeNull();
    expect(dueLabel("2025-01-01", "2026-03-10")).toBeNull();
  });
});

import {describe, expect, it} from "vitest";

import {addDaysIso, timeHm} from "./today";

describe("addDaysIso", () => {
  it("crosses month, year and leap-day edges without reading UTC", () => {
    expect(addDaysIso("2026-03-01", -1)).toBe("2026-02-28");
    expect(addDaysIso("2028-03-01", -1)).toBe("2028-02-29");
    expect(addDaysIso("2026-12-31", 1)).toBe("2027-01-01");
    expect(addDaysIso("2026-10-17", 0)).toBe("2026-10-17");
  });
});

describe("timeHm", () => {
  it("reads the São Paulo clock whatever the browser's zone", () => {
    expect(timeHm("2026-10-17T15:05:00Z")).toMatch(/^12[:h.]05/);
  });
});

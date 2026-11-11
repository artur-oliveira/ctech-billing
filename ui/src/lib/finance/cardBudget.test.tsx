import {act, renderHook} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {PERSONAL, spaceHeader} from "@/lib/console/space";
import {useCardBudget} from "@/lib/finance/cardBudget";

vi.mock("@/lib/finance/useFinanceSpaces", async () => {
  const {PERSONAL: space} = await import("@/lib/console/space");
  const ctx = {mode: "live", space};
  return {useFinanceCtx: () => ctx};
});

const KEY = `ctech-billing-card-budget:live:${spaceHeader(PERSONAL)}`;

describe("useCardBudget", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => vi.restoreAllMocks());

  it("starts on the automatic estimate (null)", () => {
    expect(renderHook(() => useCardBudget()).result.current[0]).toBeNull();
  });

  it("keeps what was set, per space, and null removes it", () => {
    const {result} = renderHook(() => useCardBudget());
    act(() => result.current[1](123400));
    expect(result.current[0]).toBe(123400);
    expect(Object.entries(window.localStorage)).toEqual([[KEY, "123400"]]);
    act(() => result.current[1](null));
    expect(result.current[0]).toBeNull();
    expect(window.localStorage.getItem(KEY)).toBeNull();
  });

  it("reads zero as a budget, and ignores anything that is not a sane amount of cents", () => {
    window.localStorage.setItem(KEY, "0");
    expect(renderHook(() => useCardBudget()).result.current[0]).toBe(0);
    for (const junk of ["abc", "-5", "12.5", "99999999999999"]) {
      window.localStorage.setItem(KEY, junk);
      expect(renderHook(() => useCardBudget()).result.current[0]).toBeNull();
    }
  });

  it("still works for the page's life when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    const {result} = renderHook(() => useCardBudget());
    expect(result.current[0]).toBeNull();
    act(() => result.current[1](5000));
    expect(result.current[0]).toBe(5000);
    act(() => result.current[1](null));
  });
});

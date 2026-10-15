import "@testing-library/jest-dom/vitest";

import {act, render, screen} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {LiveIndicator} from "@/components/realtime/LiveIndicator";
import type {RealtimeStatus} from "@/lib/realtime/core";
import {RealtimeStatusContext} from "@/lib/realtime/useRealtimeStatus";

function show(status: RealtimeStatus) {
  return render(
    <RealtimeStatusContext.Provider value={status}>
      <LiveIndicator/>
    </RealtimeStatusContext.Provider>,
  );
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("LiveIndicator", () => {
  it("is a polite status region", () => {
    show("off");
    const region = screen.getByRole("status");
    expect(region).toHaveAttribute("aria-live", "polite");
  });

  it("says nothing when realtime is off or revoked", () => {
    for (const s of ["off", "revoked"] as const) {
      const {unmount} = show(s);
      expect(screen.getByRole("status")).toBeEmptyDOMElement();
      unmount();
    }
  });

  it("says it is live", () => {
    show("live");
    expect(screen.getByRole("status")).toHaveTextContent(/ao vivo|live/i);
  });

  it("says how often it updates when polling", () => {
    show("polling");
    expect(screen.getByRole("status")).toHaveTextContent(/10 s/);
  });

  it("stays quiet while connecting for less than three seconds, then tells it is polling-paced", () => {
    show("connecting");
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
    act(() => void vi.advanceTimersByTime(2_900));
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
    act(() => void vi.advanceTimersByTime(200));
    expect(screen.getByRole("status")).toHaveTextContent(/10 s/);
  });
});

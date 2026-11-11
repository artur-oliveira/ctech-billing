import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

afterEach(() => { vi.unstubAllEnvs(); vi.resetModules(); });

describe("MockControls", () => {
  it("shrinks to a bare 'mock' on a phone and docks above the bottom bar", async () => {
    vi.stubEnv("NEXT_PUBLIC_MOCK_API", "true");
    const {MockControls} = await import("@/dev/MockControls");
    render(<QueryClientProvider client={new QueryClient()}><MockControls/></QueryClientProvider>);
    const chip = screen.getByRole("button", {name: /mock/});
    // The scenario name is for a laptop; Tailwind hides it under md.
    expect(chip.querySelector("span")).toHaveClass("max-md:hidden");
    expect(chip.parentElement).toHaveClass("max-md:bottom-[calc(4.75rem+env(safe-area-inset-bottom))]");
  });

  it("renders nothing outside mock mode", async () => {
    const {MockControls} = await import("@/dev/MockControls");
    const {container} = render(<QueryClientProvider client={new QueryClient()}><MockControls/></QueryClientProvider>);
    expect(container).toBeEmptyDOMElement();
  });
});

import "@testing-library/jest-dom/vitest";

import {act, render, screen} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {ContainerSwitch} from "@/components/ui/ContainerSwitch";

let notify: (w: number) => void = () => undefined;

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class {
    constructor(cb: (e: {contentRect: {width: number}}[]) => void) { notify = w => cb([{contentRect: {width: w}}]); }
    observe() {}
    disconnect() {}
  });
});
afterEach(() => vi.unstubAllGlobals());

const view = () => <ContainerSwitch min={600}>{narrow => <p>{narrow ? "lista" : "tabela"}</p>}</ContainerSwitch>;

describe("ContainerSwitch", () => {
  it("follows the container's width, not the window's", () => {
    render(view());
    act(() => notify(900));
    expect(screen.getByText("tabela")).toBeInTheDocument();
    act(() => notify(320));
    expect(screen.getByText("lista")).toBeInTheDocument();
    act(() => notify(600));
    expect(screen.getByText("tabela")).toBeInTheDocument();
  });
  it("falls back to the desktop layout while unmeasured", () => {
    render(view());
    expect(screen.getByText("tabela")).toBeInTheDocument();
  });
});

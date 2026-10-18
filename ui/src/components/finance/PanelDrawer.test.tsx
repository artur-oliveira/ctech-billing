import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {PanelDrawer} from "@/components/finance/PanelDrawer";

const drawer = (open: boolean, allowed?: boolean) => (
  <PanelDrawer open={open} allowed={allowed} onClose={() => {}} title="Nova coisa"><input aria-label="campo"/></PanelDrawer>
);

describe("PanelDrawer", () => {
  it("mounts its form only while open", () => {
    const {rerender} = render(drawer(false));
    expect(screen.queryByLabelText("campo")).toBeNull();
    rerender(drawer(true));
    expect(screen.getByLabelText("campo")).toBeInTheDocument();
  });

  it("never opens for a role that is not allowed, even when asked", () => {
    render(drawer(true, false));
    expect(screen.queryByLabelText("campo")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

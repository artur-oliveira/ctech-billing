import "@testing-library/jest-dom/vitest";

import {act, render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it} from "vitest";

import {PanelDrawer} from "@/components/finance/PanelDrawer";
import {changeAppLanguage} from "@/lib/i18n";

const drawer = (open: boolean, allowed?: boolean) => (
  <PanelDrawer open={open} allowed={allowed} onClose={() => {}} title="Nova coisa"><input aria-label="campo"/></PanelDrawer>
);

afterEach(async () => { await act(() => changeAppLanguage("pt-BR")); });

describe("PanelDrawer", () => {
  it("names its close button in the UI language", async () => {
    await act(() => changeAppLanguage("en"));
    render(drawer(true));
    expect(screen.getByRole("button", {name: "Close"})).toBeInTheDocument();
    expect(screen.queryByRole("button", {name: "Fechar"})).toBeNull();
  });

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

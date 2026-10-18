import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {DrawerForm, PanelActions, PanelForm} from "@/components/finance/PanelForm";

describe("PanelForm / DrawerForm", () => {
  it("the in-row form is tinted, the drawer one is not, both lay fields out in the same grid", () => {
    render(<><PanelForm aria-label="row"><input/></PanelForm><DrawerForm aria-label="drawer" columns="sm:grid-cols-3"><input/></DrawerForm></>);
    expect(screen.getByRole("form", {name: "row"})).toHaveClass("bg-surface", "sm:grid-cols-2", "items-start");
    const drawer = screen.getByRole("form", {name: "drawer"});
    expect(drawer).not.toHaveClass("bg-surface");
    expect(drawer).toHaveClass("sm:grid-cols-3", "items-start");
  });

  it("the actions row spans every column", () => {
    render(<PanelActions><button>ok</button></PanelActions>);
    expect(screen.getByRole("button").parentElement).toHaveClass("sm:col-span-full");
  });
});

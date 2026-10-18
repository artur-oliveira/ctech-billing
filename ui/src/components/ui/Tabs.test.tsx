import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {useState} from "react";
import {describe, expect, it} from "vitest";

import {panelId, Tabs, tabId} from "@/components/ui/Tabs";

const OPTIONS = [{value: "a", label: "Alfa"}, {value: "b", label: "Beta"}, {value: "c", label: "Gama"}];

function Harness() {
  const [v, setV] = useState("a");
  return <><Tabs label="Relatório" idBase="t" value={v} onValueChange={setV} options={OPTIONS}/><div role="tabpanel" id={panelId("t")} aria-labelledby={tabId("t", v)}>{v}</div></>;
}

describe("Tabs", () => {
  it("is a tablist with one tab in the tab order, linked to its panel", () => {
    render(<Harness/>);
    const tabs = screen.getAllByRole("tab");
    expect(screen.getByRole("tablist", {name: "Relatório"})).toBeInTheDocument();
    expect(tabs.map(t => t.tabIndex)).toEqual([0, -1, -1]);
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toHaveAccessibleName("Alfa");
  });
  it("moves and selects with the arrow keys, wrapping, and Home/End", async () => {
    render(<Harness/>);
    screen.getByRole("tab", {name: "Alfa"}).focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", {name: "Beta"})).toHaveFocus();
    expect(screen.getByRole("tab", {name: "Beta"})).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{End}{ArrowRight}");
    expect(screen.getByRole("tab", {name: "Alfa"})).toHaveFocus();
    await userEvent.keyboard("{ArrowLeft}");
    expect(screen.getByRole("tab", {name: "Gama"})).toHaveFocus();
    await userEvent.keyboard("{Home}");
    expect(screen.getByRole("tab", {name: "Alfa"})).toHaveAttribute("aria-selected", "true");
  });
  it("selects on click and scrolls inside its own box, not the page", async () => {
    render(<Harness/>);
    await userEvent.click(screen.getByRole("tab", {name: "Gama"}));
    expect(screen.getByRole("tabpanel")).toHaveTextContent("c");
    expect(screen.getByRole("tablist")).toHaveClass("overflow-x-auto", "max-w-full");
  });
});

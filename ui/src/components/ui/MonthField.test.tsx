import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {describe, expect, it, vi} from "vitest";

import i18n from "@/lib/i18n";

import {MonthField, monthNames, monthTitle} from "./MonthField";

describe("MonthField", () => {
  it("shows a civil month in the app's language and gives YYYY-MM back", async () => {
    const change = vi.fn();
    render(<><label htmlFor="m">Mês</label><MonthField id="m" value="2026-03" onValueChange={change}/></>);
    const trigger = screen.getByLabelText("Mês");
    expect(trigger).toHaveTextContent("março de 2026");
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByRole("button", {name: /^abr\.? 2026$/i}));
    expect(change).toHaveBeenCalledWith("2026-04");
  });

  it("walks years and disables months outside min/max", async () => {
    const change = vi.fn();
    render(<MonthField id="m" value="2026-03" min="2026-02" max="2026-05" onValueChange={change}/>);
    await userEvent.click(screen.getByRole("button", {name: /março/}));
    expect(await screen.findByRole("button", {name: /^jan\.? 2026$/i})).toBeDisabled();
    expect(screen.getByRole("button", {name: /^jun\.? 2026$/i})).toBeDisabled();
    expect(screen.getByRole("button", {name: "Próximo ano"})).toBeDisabled();
    expect(screen.getByRole("button", {name: "Ano anterior"})).toBeDisabled();
  });

  it("marks the current month pressed and the trigger invalid", async () => {
    render(<MonthField id="m" value="2026-03" invalid onValueChange={() => {}}/>);
    const trigger = screen.getByRole("button", {name: /março/});
    expect(trigger).toHaveAttribute("aria-invalid", "true");
    await userEvent.click(trigger);
    expect(await screen.findByRole("button", {name: /^mar\.? 2026$/i})).toHaveAttribute("aria-pressed", "true");
  });

  it("shows the placeholder when empty", () => {
    render(<MonthField id="m" value="" onValueChange={() => {}}/>);
    expect(screen.getByText("Escolher mês")).toBeInTheDocument();
  });

  it("renders English names when the app is English, whatever the browser says", async () => {
    await i18n.changeLanguage("en");
    try {
      render(<MonthField id="m" value="2026-03" onValueChange={() => {}}/>);
      expect(screen.getByRole("button", {name: /March 2026/})).toBeInTheDocument();
    } finally { await i18n.changeLanguage("pt-BR"); }
  });
});

describe("month helpers", () => {
  it("names twelve months, January first", () => {
    expect(monthNames("en")).toHaveLength(12);
    expect(monthNames("en")[0]).toBe("Jan");
    expect(monthTitle("2026-12", "pt-BR")).toBe("dezembro de 2026");
  });
});

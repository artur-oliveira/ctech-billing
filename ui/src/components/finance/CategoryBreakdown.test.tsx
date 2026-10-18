import "@testing-library/jest-dom/vitest";

import {render, screen, within} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import {CategoryBreakdown} from "@/components/finance/CategoryBreakdown";
import type {CategoryAmounts, CategoryMonth} from "@/lib/api/financeTypes";

const row = (category_id: string, realised: number, scheduled: number, forecast: number): CategoryAmounts =>
  ({category_id, realised, scheduled, forecast, total: realised + scheduled + forecast});
const sum = (rows: CategoryAmounts[]) => ({
  realised: rows.reduce((a, r) => a + r.realised, 0), scheduled: rows.reduce((a, r) => a + r.scheduled, 0),
  forecast: rows.reduce((a, r) => a + r.forecast, 0), total: rows.reduce((a, r) => a + r.total, 0),
});
const incomeRows = [row("sal", 500000, 0, 0)];
const expenseRows = [row("rent", 0, 150000, 0), row("food", 30000, 0, 20000)];
const DATA: CategoryMonth = {
  month: "2026-10",
  income: {totals: sum(incomeRows), categories: incomeRows},
  expense: {totals: sum(expenseRows), categories: expenseRows},
};
const names: Record<string, string> = {sal: "Salário", rent: "Aluguel", food: "Alimentação"};
const nameOf = (id: string) => names[id] ?? "Sem categoria";

afterEach(() => vi.unstubAllGlobals());

function narrow(on: boolean) {
  vi.stubGlobal("matchMedia", (q: string) => ({matches: on && q.includes("max-width"), media: q, addEventListener() {}, removeEventListener() {}}));
}

describe("CategoryBreakdown", () => {
  it("is a table from sm up: realised, scheduled and forecast per category, income apart from expense", () => {
    narrow(false);
    render(<CategoryBreakdown data={DATA} nameOf={nameOf}/>);
    const expense = screen.getByRole("region", {name: "Despesas"});
    for (const h of ["Categoria", "Realizado", "Agendado", "Previsto", "Total"]) expect(within(expense).getByRole("columnheader", {name: h})).toBeInTheDocument();
    const food = within(expense).getByRole("row", {name: /Alimentação/});
    expect(within(food).getAllByRole("cell").map(c => c.textContent)).toEqual(expect.arrayContaining([expect.stringContaining("300,00"), expect.stringContaining("200,00"), expect.stringContaining("500,00")]));
    // largest first, as the API sends them
    expect(within(expense).getAllByRole("rowheader").map(h => h.textContent)).toEqual(["Aluguel", "Alimentação", "Total"]);
    expect(screen.getByRole("region", {name: "Receitas"})).toBeInTheDocument();
  });

  it("falls back to the list when its own container is too narrow, whatever the window", async () => {
    narrow(false);
    vi.stubGlobal("ResizeObserver", class {
      constructor(private cb: (e: {contentRect: {width: number}}[]) => void) {}
      observe() { this.cb([{contentRect: {width: 500}}]); }
      disconnect() {}
    });
    render(<CategoryBreakdown data={DATA} nameOf={nameOf}/>);
    expect(await screen.findByText(/realizado R\$\s*300,00/)).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("says the share in numbers, not only in the bar", () => {
    narrow(false);
    render(<CategoryBreakdown data={DATA} nameOf={nameOf}/>);
    const rent = screen.getByRole("row", {name: /Aluguel/});
    expect(within(rent).getByText("75%")).toBeInTheDocument(); // 150000 of 200000
  });

  it("is a list on a phone, naming only the parts a category has", () => {
    narrow(true);
    render(<CategoryBreakdown data={DATA} nameOf={nameOf}/>);
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByText(/realizado R\$\s*300,00 · previsto R\$\s*200,00/)).toBeInTheDocument();
    // a single-part category has no breakdown line
    expect(screen.queryByText(/agendado R\$\s*1\.500,00$/)).toBeNull();
  });

  it("nets income against expense with what is booked and what is forecast", () => {
    narrow(false);
    render(<CategoryBreakdown data={DATA} nameOf={nameOf}/>);
    expect(screen.getByText("Resultado esperado do mês").nextElementSibling).toHaveTextContent("3.000,00");
    expect(screen.getByText(/Já lançado .*3\.200,00 · previsto .*200,00/)).toBeInTheDocument();
  });

  it("says nothing happened in an empty month, once", () => {
    narrow(false);
    const none = {totals: {realised: 0, scheduled: 0, forecast: 0, total: 0}, categories: []};
    render(<CategoryBreakdown data={{month: "2026-10", income: none, expense: none}} nameOf={nameOf}/>);
    expect(screen.getByText("Nada neste mês.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows a busy skeleton while loading and a retryable error on failure", () => {
    narrow(false);
    const retry = vi.fn();
    const {rerender, container} = render(<CategoryBreakdown loading nameOf={nameOf}/>);
    expect(container.querySelector("[aria-busy]")).not.toBeNull();
    rerender(<CategoryBreakdown error={new Error("boom")} onRetry={retry} nameOf={nameOf}/>);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    screen.getByRole("button", {name: "Tentar novamente"}).click();
    expect(retry).toHaveBeenCalled();
  });
});

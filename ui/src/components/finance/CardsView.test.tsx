import "@testing-library/jest-dom/vitest";

import {act, screen, waitFor, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {CardsView, statementState} from "@/components/finance/CardsView";
import {pick, renderWithQuery, serveFinanceMock} from "@/components/finance/finance.test-utils";
import * as finance from "@/lib/api/finance";
import * as createRequest from "@/lib/createRequest";
import type {Account, Card, CardStatement, Purchase, StatementSummary, Verb} from "@/lib/api/financeTypes";
import {shiftMonth} from "@/lib/finance/statementTimeline";

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"];
const ACCOUNTS: Account[] = [
  {id: "cc", name: "Conta corrente", class: "asset", system: false, archived: false, balance: 0},
  {id: "food", name: "Mercado", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "visa", name: "Visa", class: "liability", system: false, archived: false, balance: -30000},
  {id: "home", name: "Casa", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "old", name: "Antiga", class: "expense", dre_group: "operating_expenses", system: false, archived: true, balance: 0},
];
const CARD: Card = {id: "visa", name: "Visa", closing_day: 3, due_day: 10, paying_account_id: "cc", open_month: "2026-03", balance: -30000, archived: false};
const PURCHASE: Purchase = {
  id: "p1", description: "Geladeira", category_id: "food", date: "2026-03-01", total: 30000, refunded: false,
  installments: [{number: 1, amount: 10000, month: "2026-03"}, {number: 2, amount: 10000, month: "2026-04"}, {number: 3, amount: 10000, month: "2026-05"}],
};
const statement = (month: string, status: CardStatement["status"], number: number): CardStatement => ({
  card_id: "visa", month, status, closing_date: `${month}-03`, due_date: `${month}-10`, total: 10000,
  items: [{purchase_id: "p1", description: "Geladeira", category_id: "food", date: "2026-03-01", number, of: 3, kind: "installment", amount: 10000}],
});

const FIRST = "2025-06", LAST = "2027-06";
const summary = (month: string): StatementSummary => ({
  card_id: "visa", month, status: month < "2026-03" ? "paid" : month === "2026-03" ? "open" : "future",
  closing_date: `${month}-03`, due_date: `${month}-10`, total: 10000, items_count: 1,
});

function serve(verbs: Verb[], cards: Card[] = [CARD]) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false});
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false});
  vi.spyOn(finance, "listCards").mockResolvedValue({data: cards, has_more: false});
  vi.spyOn(finance, "listPurchases").mockResolvedValue({data: [PURCHASE], has_more: false});
  const range = vi.spyOn(finance, "listCardStatements").mockImplementation(async (_c, _id, from, count, direction) => {
    const out: StatementSummary[] = [];
    let m = direction === "forward" && from < FIRST ? FIRST : from;
    while (out.length < count && m >= FIRST && m <= LAST) { out.push(summary(m)); m = shiftMonth(m, direction === "forward" ? 1 : -1); }
    const edge = out.at(-1)?.month;
    return {data: out, has_more: direction === "forward" ? !!edge && edge < LAST : !!edge && edge > FIRST};
  });
  const items = vi.spyOn(finance, "getCardStatement").mockImplementation(async (_c, _id, month) =>
    statement(month, month === "2026-03" ? "open" : "future", month === "2026-03" ? 1 : 2));
  return {range, items};
}

/** Months each range request covered, to prove nothing loaded is asked again. */
function covered(range: ReturnType<typeof serve>["range"]): string[] {
  return range.mock.calls.flatMap(([, , from, count, direction]) =>
    Array.from({length: count}, (_, i) => shiftMonth(from, direction === "forward" ? i : -i)).filter(m => m >= FIRST && m <= LAST));
}

const inWindow = () => screen.getAllByRole("button", {name: /^Fatura de /}).filter(b => b.closest("[data-in-window]")).map(b => b.dataset.month);

const pickIn = (root: HTMLElement, label: string, option: string) => pick(label, option, root);

beforeEach(() => window.localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe("F5 — cartões", () => {
  it("opens on the card's open statement, in words, with its total", async () => {
    serve(ALL);
    renderWithQuery(<CardsView/>);
    const block = await screen.findByRole("region", {name: "Fatura Mar/26"});
    expect(await within(block).findByText("Aberta")).toBeInTheDocument();
    const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement;
    expect(within(row).getByText(/1\/3/)).toBeInTheDocument();
  });
  it("loads recurring installment estimates with the statement and adds them to its headline total", async () => {
    serve(ALL);
    const forecast = vi.spyOn(finance, "getCardStatementForecast").mockResolvedValue({data: [
      {recurrence_id: "r-food", description: "Mercado mensal", nominal: "2026-10-20", installment: 2, of: 3, amount: 10000},
      {recurrence_id: "r-coffee", description: "Café", nominal: "2026-10-21", installment: 1, of: 1, amount: 2800, pending_confirmation: true},
    ], has_more: false});
    renderWithQuery(<CardsView/>);
    const block = await screen.findByRole("region", {name: "Fatura Mar/26"});
    await waitFor(() => expect(forecast).toHaveBeenCalledWith(expect.anything(), "visa", "2026-03"));
    const forecastBlock = within(block).getByRole("region", {name: "Previsão de recorrências"});
    expect(await within(forecastBlock).findByText(/Mercado mensal/)).toBeInTheDocument();
    expect(within(forecastBlock).getByText(/Café/)).toBeInTheDocument();
    expect(within(forecastBlock).getByText(/aguardando confirmação/)).toBeInTheDocument();
    expect(within(forecastBlock).getByText(/R\$\s*100,00/)).toBeInTheDocument();
    expect(within(forecastBlock).getByText(/Já somadas ao total da fatura acima/)).toBeInTheDocument();
    expect(forecast).toHaveBeenCalledWith(expect.anything(), "visa", "2026-03");
  });
  it("shows posted plus forecast as the statement's one figure, the parts beneath it", async () => {
    const {range} = serve(ALL);
    const original = range.getMockImplementation()!;
    range.mockImplementation(async (...args) => {
      const page = await original(...args);
      return {...page, data: page.data.map(s => s.month === "2026-03" ? {...s, forecast_total: 34000, forecast_estimated: true} : s.month === "2026-04" ? {...s, forecast_total: 12000} : s)};
    });
    renderWithQuery(<CardsView/>);
    const march = await screen.findByRole("button", {name: /^Fatura de Mar\/26/});
    // 100,00 posted + 340,00 forecast: the headline is the sum, never the posted part alone.
    expect(within(march).getByText("R$ 440,00")).toBeInTheDocument();
    expect(within(march).getByText(/já lançado R\$\s*100,00 · recorrências previstas ~R\$\s*340,00 \(estimado\)/)).toBeInTheDocument();
    expect(march).toHaveAccessibleName(/R\$\s*440,00: já lançado R\$\s*100,00 mais R\$\s*340,00 em recorrências previstas \(estimado\)/);
    const april = screen.getByRole("button", {name: /^Fatura de Abr\/26/});
    expect(within(april).getByText(/recorrências previstas R\$\s*120,00$/)).toBeInTheDocument();
    expect(within(april).queryByText(/estimado/)).toBeNull();
    // A statement with nothing forecast says nothing about recurrences.
    expect(within(screen.getByRole("button", {name: /^Fatura de Fev\/26/})).queryByText(/recorrências/)).toBeNull();
  });

  it("carries the expected total in the statement header, beside the recurrences' subtotal", async () => {
    serve(ALL);
    vi.spyOn(finance, "getCardStatementForecast").mockResolvedValue({data: [
      {recurrence_id: "r-food", description: "Mercado mensal", nominal: "2026-10-20", installment: 2, of: 3, amount: 10000},
      {recurrence_id: "r-coffee", description: "Café", nominal: "2026-10-21", installment: 1, of: 1, amount: 2800, pending_confirmation: true},
    ], has_more: false});
    renderWithQuery(<CardsView/>);
    const block = await screen.findByRole("region", {name: "Fatura Mar/26"});
    const forecastBlock = await within(block).findByRole("region", {name: "Previsão de recorrências"});
    await within(forecastBlock).findByText(/Mercado mensal/);
    expect(within(forecastBlock).getByText("Recorrências previstas").parentElement).toHaveTextContent(/R\$\s*128,00/);
    // The same statement's header carries posted + forecast as one figure.
    expect(within(block).getByText("R$ 228,00")).toBeInTheDocument();
    expect(within(block).getByText(/já lançado R\$\s*100,00 · recorrências previstas ~R\$\s*128,00 \(estimado\)/)).toBeInTheDocument();
  });

  // Spec § 6.6.
  it("opens on the open statement with one before and five after, selected", async () => {
    serve(ALL);
    renderWithQuery(<CardsView/>);
    await waitFor(() => expect(inWindow()).toEqual(["2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08"]));
    const open = screen.getByRole("button", {name: /^Fatura de Mar\/26/});
    expect(open).toHaveAttribute("aria-pressed", "true");
    expect(within(open).getByText("Aberta")).toBeInTheDocument();
    expect(await screen.findByText("Geladeira")).toBeInTheDocument(); // the open statement's items, below
  });

  it("heads the card with what it owes next: the open statement, its total and its dates", async () => {
    serve(ALL);
    renderWithQuery(<CardsView/>);
    const header = await waitFor(() => {
      const el = document.querySelector<HTMLElement>("[data-card-header]")!;
      expect(within(el).getByText("Aberta · Mar/26")).toBeInTheDocument();
      return el;
    });
    expect(within(header).getByText("R$ 100,00")).toBeInTheDocument();
    expect(header.textContent).toMatch(/Fecha 03\/03 · Vence 10\/03/);
  });

  it("pages beyond the loaded edge in both directions without asking for a loaded month again", async () => {
    const {range} = serve(ALL);
    renderWithQuery(<CardsView/>);
    await waitFor(() => expect(inWindow()).toContain("2026-03"));
    for (let i = 0; i < 3; i++) await userEvent.click(screen.getByRole("button", {name: "Próximas faturas"}));
    await waitFor(() => expect(inWindow()).toContain("2026-12"));
    for (let i = 0; i < 6; i++) await userEvent.click(screen.getByRole("button", {name: "Faturas anteriores"}));
    await waitFor(() => expect((inWindow()[0] ?? "") <= "2025-09").toBe(true));
    const months = covered(range);
    expect(new Set(months).size).toBe(months.length); // every month asked once
  });

  it("stops at the card's first statement", async () => {
    serve(ALL);
    renderWithQuery(<CardsView/>);
    const earlier = await screen.findByRole("button", {name: "Faturas anteriores"});
    for (let i = 0; i < 6; i++) if (!(earlier as HTMLButtonElement).disabled) await userEvent.click(earlier);
    await waitFor(() => expect(inWindow()[0]).toBe(FIRST));
    await waitFor(() => expect(earlier).toBeDisabled());
  });

  it("moves the selection with the arrow keys and loads that statement's items", async () => {
    const {items} = serve(ALL);
    renderWithQuery(<CardsView/>);
    const open = await screen.findByRole("button", {name: /^Fatura de Mar\/26/});
    open.focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(screen.getByRole("button", {name: /^Fatura de Abr\/26/})).toHaveAttribute("aria-pressed", "true");
    await waitFor(() => expect(items).toHaveBeenCalledWith(expect.anything(), "visa", "2026-04"));
    expect(await screen.findByText(/2\/3/)).toBeInTheDocument();
  });

  it("marks an item a recurrence posted, linking to it", async () => {
    serve(ALL);
    vi.spyOn(finance, "getCardStatement").mockResolvedValue({...statement("2026-03", "open", 1),
      items: [{purchase_id: "p9", description: "Streaming", category_id: "food", date: "2026-03-02", number: 1, of: 1, kind: "installment", amount: 3990, recurrence_id: "r-stream"}]});
    renderWithQuery(<CardsView/>);
    const link = await screen.findByRole("link", {name: "Abrir a recorrência de Streaming"});
    expect(link).toHaveAttribute("href", "/finance/recurrences?recurrence=r-stream");
    expect(link).toHaveTextContent("recorrente");
  });

  it("has no em dash in what it shows", async () => {
    serve(ALL);
    renderWithQuery(<CardsView/>);
    await screen.findByText("Geladeira");
    expect(document.body.textContent).not.toContain("—");
  });

  describe("on a phone", () => {
    beforeEach(() => {
      vi.stubGlobal("matchMedia", (query: string) => ({matches: query.includes("max-width"), media: query, addEventListener: () => undefined, removeEventListener: () => undefined}));
    });
    afterEach(() => vi.unstubAllGlobals());

    const strip = () => screen.getByRole("list", {name: "Faturas do cartão"});
    // jsdom does no layout: give the strip a width and a scroll position it keeps.
    const sized = (el: HTMLElement, width: number) => {
      Object.defineProperty(el, "clientWidth", {configurable: true, value: width});
      Object.defineProperty(el, "scrollLeft", {configurable: true, writable: true, value: 0});
    };

    it("shows one statement at a time and selects the one swiped to", async () => {
      const {items} = serve(ALL);
      renderWithQuery(<CardsView/>);
      await screen.findByRole("button", {name: /^Fatura de Mar\/26/});
      const el = strip();
      sized(el, 300);
      const index = within(el).getAllByRole("button").findIndex(b => b.dataset.month === "2026-04");
      el.scrollLeft = index * 300;
      el.dispatchEvent(new Event("scroll"));
      await waitFor(() => expect(screen.getByRole("button", {name: /^Fatura de Abr\/26/})).toHaveAttribute("aria-pressed", "true"));
      await waitFor(() => expect(items).toHaveBeenCalledWith(expect.anything(), "visa", "2026-04"));
    });

    it("shows the next months' totals under the strip, so the future needs no swipe", async () => {
      serve(ALL);
      renderWithQuery(<CardsView/>);
      const label = await screen.findByText("Próximos meses");
      const row = label.parentElement!;
      expect(within(row).getAllByRole("button").map(b => b.textContent?.replace(/\s/g, " "))).toEqual(["Abr/26R$ 100,00", "Mai/26R$ 100,00", "Jun/26R$ 100,00"]);
    });

    it("opens the strip on the open statement once the statements arrive", async () => {
      // The strip mounts after the first page, with the selection already set: it must still scroll to it.
      const left = new WeakMap<Element, number>();
      vi.spyOn(HTMLOListElement.prototype, "clientWidth", "get").mockReturnValue(300);
      Object.defineProperty(HTMLOListElement.prototype, "scrollLeft", {configurable: true,
        get(this: Element) { return left.get(this) ?? 0; }, set(this: Element, v: number) { left.set(this, v); }});
      try {
        serve(ALL);
        renderWithQuery(<CardsView/>);
        await screen.findByRole("button", {name: /^Fatura de Mar\/26/});
        const el = strip();
        const index = within(el).getAllByRole("button").findIndex(b => b.dataset.month === "2026-03");
        await waitFor(() => expect(el.scrollLeft).toBe(index * 300));
      } finally {
        delete (HTMLOListElement.prototype as unknown as Record<string, unknown>).scrollLeft;
      }
    });

    // Final review: Chrome itself re-snaps a scroll-snap strip to the element it
    // showed when nodes are inserted before it, during the layout a read of
    // clientWidth forces. A correction that adds the shift on top of that moves
    // the strip a page too far.
    it("keeps the card on screen when the browser re-snaps the strip itself", async () => {
      serve(ALL);
      renderWithQuery(<CardsView/>);
      await screen.findByRole("button", {name: /^Fatura de Mar\/26/});
      const el = strip();
      const months = () => within(el).getAllByRole("button").map(b => b.dataset.month!);
      let left = 0;
      let snapped: string | undefined;
      // Any layout read (scrollLeft or clientWidth) re-snaps to the element that was on screen.
      const layout = () => { const i = snapped ? months().indexOf(snapped) : -1; if (i >= 0) left = i * 300; };
      Object.defineProperty(el, "scrollLeft", {configurable: true,
        get: () => { layout(); return left; }, set: (v: number) => { left = v; snapped = months()[Math.round(v / 300)]; }});
      Object.defineProperty(el, "clientWidth", {configurable: true, get: () => { layout(); return 300; }});
      el.scrollLeft = 0;
      el.dispatchEvent(new Event("scroll"));
      const shown = months()[0];
      await waitFor(() => expect(months()[0]).not.toBe(shown));
      expect(left).toBe(months().indexOf(shown) * 300);
    });

    // Review Focus 5.
    it("keeps the card on screen when older statements load on a phone", async () => {
      serve(ALL);
      renderWithQuery(<CardsView/>);
      await screen.findByRole("button", {name: /^Fatura de Mar\/26/});
      const el = strip();
      sized(el, 300);
      el.scrollLeft = 0; // the first loaded statement: the edge, so older ones load
      el.dispatchEvent(new Event("scroll"));
      const shown = within(el).getAllByRole("button")[0].dataset.month!;
      await waitFor(() => expect(within(el).getAllByRole("button")[0].dataset.month).not.toBe(shown));
      const index = within(el).getAllByRole("button").findIndex(b => b.dataset.month === shown);
      expect(el.scrollLeft).toBe(index * 300);
    });
  });

  it("edits a purchase's description and category in place, sending only what changed", async () => {
    serve(ALL);
    const patch = vi.spyOn(finance, "patchPurchase").mockResolvedValue({...PURCHASE, description: "Geladeira Brastemp", category_id: "home"});
    renderWithQuery(<CardsView/>);
    const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", {name: "Editar compra"}));
    const form = within(row).getByRole("form", {name: "Editar compra"});
    const desc = within(form).getByLabelText(/Descrição/);
    expect(desc).toHaveValue("Geladeira");
    // Only the active expense categories; amounts and installments are not offered.
    await userEvent.click(within(form).getByRole("combobox", {name: "Categoria"}));
    expect((await screen.findAllByRole("option")).map(o => o.textContent?.trim())).toEqual(["Casa", "Mercado"]);
    await userEvent.keyboard("{Escape}");
    expect(within(form).queryByLabelText(/Valor/)).not.toBeInTheDocument();
    await userEvent.clear(desc);
    await userEvent.type(desc, "  Geladeira Brastemp ");
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}));
    await waitFor(() => expect(patch).toHaveBeenCalledWith(expect.anything(), "visa", "p1", {description: "Geladeira Brastemp"}, expect.any(String)));
    await waitFor(() => expect(within(row).queryByRole("form")).not.toBeInTheDocument());
  });

  it("moves a purchase to another category", async () => {
    serve(ALL);
    const patch = vi.spyOn(finance, "patchPurchase").mockResolvedValue({...PURCHASE, category_id: "home"});
    renderWithQuery(<CardsView/>);
    const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", {name: "Editar compra"}));
    const form = within(row).getByRole("form", {name: "Editar compra"});
    await pickIn(form, "Categoria", "Casa");
    expect(within(form).getByText(/O valor inteiro passa para a nova categoria/)).toBeInTheDocument();
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}));
    await waitFor(() => expect(patch).toHaveBeenCalledWith(expect.anything(), "visa", "p1", {category_id: "home"}, expect.any(String)));
  });

  it("renames the purchase on its statement through the dev mock", async () => {
    serveFinanceMock();
    renderWithQuery(<CardsView/>);
    const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", {name: "Editar compra"}));
    const desc = within(row).getByLabelText(/Descrição/);
    await userEvent.clear(desc);
    await userEvent.type(desc, "Geladeira nova");
    await userEvent.click(within(row).getByRole("button", {name: "Salvar"}));
    expect(await screen.findByText("Geladeira nova")).toBeInTheDocument();
  });

  it("refunds a purchase after confirming", async () => {
    serve(ALL);
    const refund = vi.spyOn(finance, "refundPurchase").mockResolvedValue({...PURCHASE, refunded: true});
    renderWithQuery(<CardsView/>);
    const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", {name: "Estornar compra"}));
    expect(within(row).getByText(/As parcelas já cobradas voltam como crédito/)).toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", {name: "Confirmar"}));
    await waitFor(() => expect(refund).toHaveBeenCalledWith(expect.anything(), "visa", "p1", expect.any(String)));
  });

  // A write refreshes the timeline: TanStack refetches an infinite query from its
  // FIRST page (the older statements, once they were prefetched) and walks
  // forward from there. The row must still show the same statements after it.
  for (const [name, label, mock] of [
    ["a refund", "Estornar compra", "refundPurchase"],
    ["an advance", "Antecipar parcelas", "advancePurchase"],
  ] as const) {
    it(`keeps the statement row whole after ${name}`, async () => {
      const {range} = serve(ALL);
      vi.spyOn(finance, mock).mockResolvedValue({...PURCHASE});
      renderWithQuery(<CardsView/>);
      const row = (await screen.findByText("Geladeira")).closest("li") as HTMLElement;
      await waitFor(() => expect(range.mock.calls.length).toBeGreaterThanOrEqual(2)); // the older page, prefetched
      await new Promise(r => setTimeout(r, 300)); // the prefetches settle
      const before = inWindow();
      const calls = range.mock.calls.length;
      await userEvent.click(within(row).getByRole("button", {name: label}));
      await userEvent.click(within(row).getByRole("button", {name: "Confirmar"}));
      const loaded = range.mock.calls.slice(0, calls).map(([, , from, count, direction]) => `${direction} ${from} ${count}`).sort();
      await waitFor(() => expect(range.mock.calls.length).toBe(2 * calls)); // refreshed
      await waitFor(() => expect(screen.getAllByRole("button", {name: /^Fatura de /}).length).toBeGreaterThanOrEqual(10));
      // Exactly the pages loaded before, each asked once more.
      expect(range.mock.calls.slice(calls).map(([, , from, count, direction]) => `${direction} ${from} ${count}`).sort()).toEqual(loaded);
      expect(inWindow()).toEqual(before);
      expect(screen.getByRole("button", {name: /^Fatura de Mar\/26/})).toHaveAttribute("aria-pressed", "true");
    });
  }

  // The reported bug, end to end through the dev mock: an installment purchase
  // emptied the row until a reload.
  it("keeps the statement row after an installment purchase, with the new totals", async () => {
    serveFinanceMock();
    renderWithQuery(<CardsView/>);
    const open = await screen.findByRole("button", {name: /^Fatura de .* Aberta/});
    const month = open.dataset.month!;
    await waitFor(() => expect(inWindow().length).toBeGreaterThan(3));
    const before = inWindow();
    await userEvent.click(screen.getByRole("button", {name: "Nova compra"}));
    const form = await screen.findByRole("form", {name: "Nova compra"});
    await userEvent.type(within(form).getByLabelText(/Descrição/), "Sofá");
    await userEvent.type(within(form).getByLabelText(/Valor/), "300,00");
    await pickIn(form, "Parcelas", "3×");
    await pickIn(form, "Categoria", "Mercado");
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}));
    // Bought after this month's closing day or not, one of the next two statements gains R$ 100,00.
    const totals = () => screen.getAllByRole("button", {name: /^Fatura de /}).map(b => b.getAttribute("aria-label"));
    await waitFor(() => expect(totals().some(n => /(433|400),00(,|$| mais)/.test(n ?? ""))).toBe(true));
    expect(inWindow()).toEqual(before);
    expect(screen.getByRole("button", {name: /Aberta/})).toHaveAttribute("data-month", month);
  });

  it("hides every action from a viewer", async () => {
    serve(["finance.read"]);
    renderWithQuery(<CardsView/>);
    await screen.findByText("Geladeira");
    for (const name of ["Nova compra", "Editar compra", "Estornar compra", "Antecipar parcelas", "Fechar fatura agora", "Novo cartão"]) {
      expect(screen.queryByRole("button", {name})).not.toBeInTheDocument();
    }
  });

  it("teaches the first step when there is no card", async () => {
    serve(ALL, []);
    renderWithQuery(<CardsView/>);
    expect(await screen.findByText("Nenhum cartão ainda.")).toBeInTheDocument();
    expect(screen.getByRole("button", {name: "Novo cartão"})).toBeInTheDocument();
  });
});

describe("F5 — a card's brand and last four digits (UX batch 3)", () => {
  const BRANDED: Card = {...CARD, brand: "mastercard", last4: "4242"};

  it("shows the card's mark, name and last digits in its header and in the picker", async () => {
    serve(ALL, [BRANDED, {...CARD, id: "elo", name: "Vivo Elo", brand: "elo"}]);
    renderWithQuery(<CardsView/>);
    const header = await screen.findByRole("heading", {name: /Visa/});
    expect(within(header).getByText("•••• 4242")).toBeInTheDocument();
    expect(header.closest("[data-card-header]")?.querySelector("svg[data-brand=mastercard]")).not.toBeNull();
    await userEvent.click(screen.getByRole("combobox", {name: "Cartão"}));
    const option = await screen.findByRole("option", {name: /Vivo Elo/});
    expect(option.querySelector("svg[data-brand=elo]")).not.toBeNull();
  });

  it("creates a card with its brand, picked by mark and name, and its last four digits", async () => {
    serve(ALL, []);
    const create = vi.spyOn(finance, "createCard").mockResolvedValue({...BRANDED, id: "n"});
    renderWithQuery(<CardsView/>);
    await userEvent.click(await screen.findByRole("button", {name: "Novo cartão"}));
    const form = screen.getByRole("form", {name: "Novo cartão"});
    await userEvent.type(within(form).getByLabelText(/^Nome/), "Nubank");
    await userEvent.click(within(form).getByRole("combobox", {name: "Bandeira"}));
    const options = await screen.findAllByRole("option");
    // By accessible name: the mark is aria-hidden decoration beside the name.
    // Nenhuma first (UX batch 4): a brand is optional and can be taken back.
    const names = ["Nenhuma", "Visa", "Mastercard", "Elo", "American Express", "Hipercard", "Diners Club", "Outra"];
    expect(options).toHaveLength(names.length);
    names.forEach((name, i) => expect(options[i]).toBe(screen.getByRole("option", {name})));
    for (const o of options.slice(1)) expect(o.querySelector("svg[data-brand]")).not.toBeNull();
    await userEvent.click(screen.getByRole("option", {name: "Mastercard"}));
    // Only digits, at most four.
    await userEvent.type(within(form).getByLabelText(/Últimos 4 dígitos/), "42a425");
    expect(within(form).getByLabelText(/Últimos 4 dígitos/)).toHaveValue("4242");
    await pickIn(form, "Fecha no dia", "3");
    await pickIn(form, "Vence no dia", "10");
    await pickIn(form, "Pagar com", "Conta corrente");
    await userEvent.click(within(form).getByRole("button", {name: "Criar cartão"}));
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.anything(),
      expect.objectContaining({name: "Nubank", brand: "mastercard", last4: "4242"}), expect.any(String)));
  });

  it("refuses fewer than four digits in words, before calling the server", async () => {
    serve(ALL, []);
    const create = vi.spyOn(finance, "createCard");
    renderWithQuery(<CardsView/>);
    await userEvent.click(await screen.findByRole("button", {name: "Novo cartão"}));
    const form = screen.getByRole("form", {name: "Novo cartão"});
    await userEvent.type(within(form).getByLabelText(/^Nome/), "Nubank");
    await userEvent.type(within(form).getByLabelText(/Últimos 4 dígitos/), "42");
    await pickIn(form, "Fecha no dia", "3");
    await pickIn(form, "Vence no dia", "10");
    await pickIn(form, "Pagar com", "Conta corrente");
    await userEvent.click(within(form).getByRole("button", {name: "Criar cartão"}));
    expect(await within(form).findByText("Digite os 4 últimos dígitos, ou deixe em branco.")).toBeInTheDocument();
    expect(create).not.toHaveBeenCalled();
  });

  it("edits the brand and clears the digits", async () => {
    serve(ALL, [BRANDED]);
    const patch = vi.spyOn(finance, "patchCard").mockResolvedValue({...BRANDED, brand: "visa", last4: undefined});
    renderWithQuery(<CardsView/>);
    await userEvent.click(await screen.findByRole("button", {name: "Editar cartão"}));
    const form = screen.getByRole("form", {name: "Editar cartão"});
    expect(within(form).getByRole("combobox", {name: "Bandeira"})).toHaveTextContent("Mastercard");
    expect(within(form).getByLabelText(/Últimos 4 dígitos/)).toHaveValue("4242");
    await pickIn(form, "Bandeira", "Visa");
    await userEvent.clear(within(form).getByLabelText(/Últimos 4 dígitos/));
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}));
    await waitFor(() => expect(patch).toHaveBeenCalledWith(expect.anything(), "visa",
      expect.objectContaining({brand: "visa", last4: ""}), expect.any(String)));
  });

  it("leaves the name out of a patch that does not change it", async () => {
    serve(ALL);
    const patch = vi.spyOn(finance, "patchCard").mockResolvedValue({...CARD, due_day: 12});
    renderWithQuery(<CardsView/>);
    await userEvent.click(await screen.findByRole("button", {name: "Editar cartão"}));
    const form = screen.getByRole("form", {name: "Editar cartão"});
    await pickIn(form, "Vence no dia", "12");
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}));
    await waitFor(() => expect(patch).toHaveBeenCalled());
    expect(patch.mock.calls[0][2]).not.toHaveProperty("name");
    expect(patch.mock.calls[0][2]).toMatchObject({due_day: 12});
  });

  it("keeps a name renamed elsewhere while its edit form was open", async () => {
    serve(ALL);
    const patch = vi.spyOn(finance, "patchCard").mockResolvedValue(CARD);
    const {client} = renderWithQuery(<CardsView/>);
    await userEvent.click(await screen.findByRole("button", {name: "Editar cartão"}));
    const form = screen.getByRole("form", {name: "Editar cartão"});
    // Renamed on Contas (or another tab) while this form is open: the list refreshes.
    vi.mocked(finance.listCards).mockResolvedValue({data: [{...CARD, name: "Visa Platinum"}], has_more: false});
    await act(() => client.invalidateQueries());
    await waitFor(() => expect(screen.getAllByText(/Visa Platinum/).length).toBeGreaterThan(0));
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}));
    await waitFor(() => expect(patch).toHaveBeenCalled());
    expect(patch.mock.calls[0][2]).not.toHaveProperty("name");
  });

  it("renames the card from its edit form, trimmed", async () => {
    serve(ALL);
    const patch = vi.spyOn(finance, "patchCard").mockResolvedValue({...CARD, name: "Visa Infinite"});
    renderWithQuery(<CardsView/>);
    await userEvent.click(await screen.findByRole("button", {name: "Editar cartão"}));
    const form = screen.getByRole("form", {name: "Editar cartão"});
    const name = within(form).getByLabelText(/^Nome/);
    expect(name).toHaveValue("Visa");
    await userEvent.clear(name);
    expect(within(form).getByRole("button", {name: "Salvar"})).toBeDisabled();
    await userEvent.type(name, " Visa Infinite  ");
    await userEvent.click(within(form).getByRole("button", {name: "Salvar"}));
    await waitFor(() => expect(patch).toHaveBeenCalledWith(expect.anything(), "visa",
      expect.objectContaining({name: "Visa Infinite", closing_day: 3, due_day: 10}), expect.any(String)));
  });
});

describe("F5 — statement status colours (UX batch 3)", () => {
  beforeEach(() => {
    vi.useFakeTimers({toFake: ["Date"]});
    vi.setSystemTime(new Date(2026, 2, 20, 9, 0)); // 20 March 2026
  });
  afterEach(() => vi.useRealTimers());

  // The selected statement's own badge, under the timeline.
  const badge = async (text: string) =>
    (await within(await screen.findByRole("region", {name: /^Fatura /})).findByText(text)).closest("[data-slot=badge], span") as HTMLElement;

  it("an open statement is calm green, never a warning", async () => {
    serve(ALL);
    renderWithQuery(<CardsView/>);
    const b = await badge("Aberta");
    expect(b).toHaveClass("text-success");
    expect(b).not.toHaveClass("text-warning");
    expect(b.querySelector("svg")).not.toBeNull(); // a glyph, never colour alone
  });

  it("a closed statement not yet due is neutral; red only once it is past due unpaid", async () => {
    const get = serve(ALL).items;
    get.mockImplementation(async (_c, _id, month) => ({
      ...statement(month, "closed", 1), bill_id: "b1",
      due_date: month === "2026-03" ? "2026-03-25" : "2026-03-10",
    }));
    renderWithQuery(<CardsView/>);
    const closed = await badge("Fechada");
    expect(closed).not.toHaveClass("text-danger");
    expect(closed).not.toHaveClass("text-warning");
    await userEvent.click(await screen.findByRole("button", {name: /^Fatura de Fev\/26/}));
    const late = await badge("Vencida");
    expect(late).toHaveClass("text-danger");
  });
});

// Review fix: a zero or credit statement makes no bill, and every month before a
// card's first one reads "closed"; none of them owes anything, so none is Vencida.
describe("statementState: Vencida only when something is owed", () => {
  const today = "2026-03-20";
  const s = (over: Partial<CardStatement>): CardStatement => ({...statement("2026-02", "closed", 1), due_date: "2026-02-10", total: 10000, bill_id: "b1", ...over});

  it("an empty statement past its due date is not Vencida", () => {
    expect(statementState(s({total: 0, bill_id: undefined, items: []}), today)).toBe("closed");
  });
  it("a credit statement past its due date is not Vencida", () => {
    expect(statementState(s({total: -5000, bill_id: undefined}), today)).toBe("closed");
  });
  it("a month before the card's first is not Vencida", () => {
    expect(statementState(s({month: "2025-11", due_date: "2025-11-10", total: 0, bill_id: undefined, items: []}), today)).toBe("closed");
  });
  it("a closed, unpaid, past-due statement with a bill is Vencida", () => {
    expect(statementState(s({}), today)).toBe("overdue");
    expect(statementState(s({due_date: "2026-03-25"}), today)).toBe("closed"); // not due yet
    expect(statementState(s({status: "paid"}), today)).toBe("paid");
  });
});

describe("the phone's central action", () => {
  it("does not open Nova compra for a role that cannot create", async () => {
    serve(["finance.read"]);
    createRequest.requestCreate("purchase");
    renderWithQuery(<CardsView/>);
    await new Promise(r => setTimeout(r, 50));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("opens Nova compra on the card on screen, once the cards are known", async () => {
    serve(ALL);
    createRequest.requestCreate("purchase");
    renderWithQuery(<CardsView/>);
    expect(await screen.findByRole("dialog", {name: "Nova compra"})).toBeInTheDocument();
  });

  it("opens Novo cartão instead when there is no card to buy with", async () => {
    serve(ALL, []);
    createRequest.requestCreate("purchase");
    renderWithQuery(<CardsView/>);
    expect(await screen.findByRole("dialog", {name: "Novo cartão"})).toBeInTheDocument();
  });
});

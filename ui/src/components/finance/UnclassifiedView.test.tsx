import "@testing-library/jest-dom/vitest";

import {fireEvent, screen, waitFor, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it, vi} from "vitest";

import {UnclassifiedView} from "@/components/finance/UnclassifiedView";
import {pick, renderWithQuery} from "@/components/finance/finance.test-utils";
import * as connections from "@/lib/api/connections";
import * as finance from "@/lib/api/finance";
import type {Account, Verb} from "@/lib/api/financeTypes";
import type {UnclassifiedLine} from "@/lib/api/connectionsTypes";

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"];
const ACCOUNTS: Account[] = [
  {id: "byok-inter-1", name: "Inter ···4567", class: "asset", system: false, archived: false, balance: 1500, source: "byok", provider: "inter"},
  {id: "cat-outras-receitas", name: "Outras receitas", class: "income", dre_group: "other", system: false, archived: false, balance: 0},
  {id: "cat-aluguel", name: "Aluguel", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "caixa", name: "Caixa", class: "asset", system: false, archived: false, balance: 0},
];

function renderUnclassified(lines: UnclassifiedLine[], verbs: Verb[] = ALL) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false});
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false});
  vi.spyOn(finance, "listBills").mockResolvedValue({data: [], has_more: false});
  // The server forgets a classified line: the second read is empty.
  vi.spyOn(connections, "listUnclassified").mockResolvedValueOnce({data: lines}).mockResolvedValue({data: []});
  return renderWithQuery(<UnclassifiedView account="byok-inter-1"/>);
}

afterEach(() => vi.restoreAllMocks());

describe("A classificar", () => {
  it("classifies a line into a category and removes it from the list", async () => {
    const classify = vi.spyOn(connections, "classify").mockResolvedValue({tx_id: "tx2"});
    renderUnclassified([{tx_id: "tx1", date: "2026-10-01", amount: 1500, direction: "credit", memo: "Pix recebido"}]);
    expect(await screen.findByText("Pix recebido")).toBeInTheDocument();
    await pick("Categoria", "Outras receitas");
    fireEvent.click(screen.getByRole("button", {name: "Classificar"}));
    await waitFor(() => expect(classify).toHaveBeenCalledWith(expect.anything(), "tx1", {category_id: "cat-outras-receitas"}, expect.any(String)));
    expect(await screen.findByText("Tudo classificado")).toBeInTheDocument();
  });

  it("offers only categories of the line's direction", async () => {
    renderUnclassified([{tx_id: "tx1", date: "2026-10-01", amount: 1500, direction: "debit", memo: "Tarifa"}]);
    await screen.findByText("Tarifa");
    fireEvent.click(screen.getByRole("combobox", {name: "Categoria"}));
    expect(await screen.findByRole("option", {name: "Aluguel"})).toBeInTheDocument();
    expect(screen.queryByRole("option", {name: "Outras receitas"})).toBeNull();
  });
});

const MANY: UnclassifiedLine[] = [
  {tx_id: "tx1", date: "2026-10-01", amount: 1500, direction: "debit", memo: "Tarifa A"},
  {tx_id: "tx2", date: "2026-10-02", amount: 1700, direction: "debit", memo: "Tarifa B"},
  {tx_id: "tx3", date: "2026-10-03", amount: 9000, direction: "credit", memo: "Pix recebido"},
];

const bulk = () => screen.getByRole("region", {name: "Classificar em massa"});

/** A server that remembers: a classified line is gone from the next read. */
function renderServing(lines: UnclassifiedLine[], classifyImpl?: (id: string, key: string) => Promise<void>) {
  const live = [...lines];
  const calls: {id: string; body: unknown; key: string}[] = [];
  vi.spyOn(connections, "classify").mockImplementation(async (_ctx, id, body, key) => {
    calls.push({id, body, key});
    await classifyImpl?.(id, key);
    const i = live.findIndex(l => l.tx_id === id);
    if (i >= 0) live.splice(i, 1);
    return {tx_id: id};
  });
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ALL, manage_people: false}], organizations_unavailable: false});
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false});
  vi.spyOn(finance, "listBills").mockResolvedValue({data: [], has_more: false});
  vi.spyOn(connections, "listUnclassified").mockImplementation(async () => ({data: [...live]}));
  return {calls, ...renderWithQuery(<UnclassifiedView account="byok-inter-1"/>)};
}
const check = async (memo: string) => userEvent.click(await screen.findByRole("checkbox", {name: `Selecionar ${memo}`}));

describe("Classificar em massa", () => {
  it("classifies the selected lines the same way, one by one, and removes them", async () => {
    const {calls} = renderServing(MANY);
    await screen.findByText("Tarifa A");
    await check("Tarifa A");
    await check("Tarifa B");
    expect(within(bulk()).getByText("2 selecionados")).toBeInTheDocument();
    await pick("Categoria dos selecionados", "Aluguel", bulk());
    await userEvent.click(within(bulk()).getByRole("button", {name: "Classificar 2 lançamentos"}));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Aluguel/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", {name: /^Classificar \d+ lançamentos?$/}));

    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls.map(c => c.id).sort()).toEqual(["tx1", "tx2"]);
    for (const c of calls) expect(c.body).toEqual({category_id: "cat-aluguel"});
    await waitFor(() => expect(screen.queryByText("Tarifa A")).toBeNull());
    expect(screen.queryByText("Tarifa B")).toBeNull();
    expect(screen.getByText("Pix recebido")).toBeInTheDocument();
  });

  it("shows how far it is while it runs", async () => {
    const release: (() => void)[] = [];
    renderServing(MANY, () => new Promise<void>(r => release.push(r)));
    await screen.findByText("Tarifa A");
    await check("Tarifa A");
    await check("Tarifa B");
    await pick("Categoria dos selecionados", "Aluguel", bulk());
    await userEvent.click(within(bulk()).getByRole("button", {name: "Classificar 2 lançamentos"}));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", {name: /^Classificar \d+ lançamentos?$/}));

    const bar = await within(bulk()).findByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuemax", "2");
    expect(bar).toHaveAttribute("aria-valuenow", "0");
    expect(within(bulk()).getByText("Classificando 0 de 2…")).toBeInTheDocument();
    await waitFor(() => expect(release).toHaveLength(2));
    release[0]();
    await waitFor(() => expect(within(bulk()).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "1"));
    release[1]();
  });

  it("selects every line at once", async () => {
    renderUnclassified(MANY);
    await screen.findByText("Tarifa A");
    await userEvent.click(await screen.findByRole("checkbox", {name: "Selecionar todos"}));
    expect(within(bulk()).getByText("3 selecionados")).toBeInTheDocument();
  });

  it("keeps a bar with the count and the way to the controls once something is chosen", async () => {
    renderUnclassified(MANY);
    await screen.findByText("Tarifa A");
    expect(screen.queryByRole("button", {name: /^Classificar 1 lançamento…$/})).toBeNull();
    await check("Tarifa A");
    expect(screen.getByRole("button", {name: "Classificar 1 lançamento…"})).toBeInTheDocument();
  });

  it("marks select-all as partly checked when only some lines are", async () => {
    renderUnclassified(MANY);
    await screen.findByText("Tarifa A");
    const all = screen.getByRole("checkbox", {name: "Selecionar todos"});
    expect(all).toHaveAttribute("aria-checked", "false");
    await check("Tarifa A");
    expect(all).toHaveAttribute("aria-checked", "mixed");
    await userEvent.click(all);
    expect(all).toHaveAttribute("aria-checked", "true");
  });

  it("picks the one open bill of the same amount, so one tap classifies", async () => {
    const classify = vi.spyOn(connections, "classify").mockResolvedValue({tx_id: "x"});
    renderUnclassified([{tx_id: "tx1", date: "2026-10-01", amount: 1500, direction: "debit", memo: "Tarifa"}]);
    vi.spyOn(finance, "listBills").mockResolvedValue({data: [{id: "b1", direction: "payable", amount: 1500, account_id: "caixa", category_id: "cat-aluguel", description: "Tarifa mensal",
      competence_date: "2026-10-02", due_date: "2026-10-02", status: "forecast", origin: "manual", auto_settle: false}], has_more: false});
    await screen.findByText("Tarifa");
    await pick("Classificar como", "Conta em aberto");
    await waitFor(() => expect(screen.getByRole("button", {name: "Classificar"})).toBeEnabled());
    fireEvent.click(screen.getByRole("button", {name: "Classificar"}));
    await waitFor(() => expect(classify).toHaveBeenCalledWith(expect.anything(), "tx1", {bill_id: "b1"}, expect.any(String)));
  });

  it("says a failed classification in the API's own words, under the row", async () => {
    vi.spyOn(connections, "classify").mockRejectedValue({response: {status: 409, data: {status: 409, code: "unclassified_gone", title: "x"}}});
    renderUnclassified([{tx_id: "tx1", date: "2026-10-01", amount: 1500, direction: "credit", memo: "Pix"}]);
    await screen.findByText("Pix");
    await pick("Categoria", "Outras receitas");
    fireEvent.click(screen.getByRole("button", {name: "Classificar"}));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });

  it("offers only a transfer for entradas and saídas together", async () => {
    renderUnclassified(MANY);
    await screen.findByText("Tarifa A");
    await check("Tarifa A");
    await check("Pix recebido");
    expect(within(bulk()).getByText(/só podem ir para uma transferência/)).toBeInTheDocument();
    fireEvent.click(within(bulk()).getByRole("combobox", {name: "Classificar selecionados como"}));
    const options = (await screen.findAllByRole("option")).map(o => o.textContent);
    expect(options).toEqual(["Transferência"]);
  });

  it("keeps the lines that failed, says so, and tries only those again with the same key", async () => {
    const keys: Record<string, string[]> = {};
    let failTx2 = true;
    renderServing(MANY, async (id, key) => {
      (keys[id] ??= []).push(key);
      if (id === "tx2" && failTx2) throw new Error("a conta foi arquivada");
    });
    await screen.findByText("Tarifa A");
    await check("Tarifa A");
    await check("Tarifa B");
    await pick("Categoria dos selecionados", "Aluguel", bulk());
    await userEvent.click(within(bulk()).getByRole("button", {name: "Classificar 2 lançamentos"}));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", {name: /^Classificar \d+ lançamentos?$/}));

    expect(await within(bulk()).findByText("1 não foi classificado")).toBeInTheDocument();
    expect(within(bulk()).getByText(/Tarifa B/)).toBeInTheDocument();
    expect(screen.queryByText("Tarifa A")).toBeNull();
    expect(screen.getByText("Tarifa B")).toBeInTheDocument();

    failTx2 = false;
    await userEvent.click(within(bulk()).getByRole("button", {name: "Tentar de novo os que falharam"}));
    await waitFor(() => expect(screen.queryByText("Tarifa B")).toBeNull());
    expect(keys.tx1).toHaveLength(1);
    expect(keys.tx2).toHaveLength(2);
    expect(keys.tx2[0]).toBe(keys.tx2[1]);
  });

  it("offers no selection to someone who cannot classify", async () => {
    renderUnclassified(MANY, ["finance.read"]);
    await screen.findByText("Tarifa A");
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.queryByRole("region", {name: "Classificar em massa"})).toBeNull();
  });
});

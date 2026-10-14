import "@testing-library/jest-dom/vitest";

import {fireEvent, screen, waitFor} from "@testing-library/react";
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

function renderUnclassified(lines: UnclassifiedLine[]) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ALL, manage_people: false}], organizations_unavailable: false});
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

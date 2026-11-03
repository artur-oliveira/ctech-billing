import "@testing-library/jest-dom/vitest";

import {screen, waitFor} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it, vi} from "vitest";

import {CreditPanel} from "@/components/finance/CreditPanel";
import {pick, renderWithQuery} from "@/components/finance/finance.test-utils";
import * as finance from "@/lib/api/finance";
import type {Account} from "@/lib/api/financeTypes";

const ACCOUNTS: Account[] = [
  {id: "food", name: "Mercado", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "salary", name: "Salário", class: "income", dre_group: "gross_revenue", system: false, archived: false, balance: 0},
];

afterEach(() => vi.restoreAllMocks());

describe("lançar crédito no cartão", () => {
  it("needs an amount and an expense category before it can be saved", async () => {
    vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ["finance.read", "finance.write"], manage_people: false}], organizations_unavailable: false});
    const add = vi.spyOn(finance, "addCardCredit").mockResolvedValue({id: "c1", description: "", category_id: "food", date: "2026-03-10", amount: 500});
    renderWithQuery(<CreditPanel cardId="inter" accounts={ACCOUNTS} onDone={() => {}}/>);
    const save = await screen.findByRole("button", {name: "Lançar"});
    expect(save).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/Valor/), "5,00");
    expect(save).toBeDisabled(); // still no category
    await pick("Categoria", "Mercado");
    expect(screen.queryByRole("option", {name: "Salário"})).not.toBeInTheDocument(); // an income category is not offered
    expect(save).toBeEnabled();
    await userEvent.click(save);
    await waitFor(() => expect(add).toHaveBeenCalledWith(expect.anything(), "inter", expect.objectContaining({category_id: "food", amount: 500}), expect.any(String)));
  });
});

import "@testing-library/jest-dom/vitest";

import {screen, waitFor} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it, vi} from "vitest";

import {renderWithQuery} from "@/components/finance/finance.test-utils";
import {PendingOccurrenceAction, validAmount} from "@/components/finance/PendingOccurrenceAction";
import * as finance from "@/lib/api/finance";
import type {PendingOccurrence, Verb} from "@/lib/api/financeTypes";

const verbs: Verb[] = ["finance.read", "finance.write", "finance.settle"];
const ITEM: PendingOccurrence = {recurrence_id: "luz", nominal: "2026-10-12", due: "2026-10-12", direction: "payable", expected_amount: 28000, category_id: "c", description: "Luz"};

function setup() {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false});
  return renderWithQuery(<PendingOccurrenceAction item={ITEM}/>);
}

afterEach(() => vi.restoreAllMocks());

describe("PendingOccurrenceAction", () => {
  it("accepts only a positive amount the API takes", () => {
    expect(validAmount(null)).toBe(false);
    expect(validAmount(0)).toBe(false);
    expect(validAmount(1)).toBe(true);
    expect(validAmount(1e15)).toBe(false);
  });

  it("confirms the prefilled expected amount in one tap", async () => {
    const confirm = vi.spyOn(finance, "confirmExpectedOccurrence").mockResolvedValue({state: "confirmed", amount: 28000, bill_id: "b"});
    setup();
    await userEvent.click(await screen.findByRole("button", {name: "Confirmar valor"}));
    await waitFor(() => expect(confirm).toHaveBeenCalledWith(expect.anything(), "luz", "2026-10-12", 28000, expect.any(String)));
  });

  it("says what a different amount does to the month, and blocks an empty or zero one", async () => {
    setup();
    const input = await screen.findByLabelText("Valor efetivo");
    await userEvent.clear(input);
    await userEvent.type(input, "300");
    expect(screen.getByText(/R\$\s*20,00 acima do previsto/)).toBeInTheDocument();
    await userEvent.clear(input);
    await userEvent.type(input, "250");
    expect(screen.getByText(/R\$\s*30,00 abaixo do previsto/)).toBeInTheDocument();
    await userEvent.clear(input);
    await userEvent.type(input, "0");
    expect(screen.getByText("Informe um valor maior que zero.")).toBeInTheDocument();
    expect(screen.getByRole("button", {name: "Confirmar valor"})).toBeDisabled();
  });

  it("skips only after the inline question, and can back out", async () => {
    const skip = vi.spyOn(finance, "skipExpectedOccurrence").mockResolvedValue({state: "skipped"} as never);
    setup();
    await userEvent.click(await screen.findByRole("button", {name: "Ignorar"}));
    expect(skip).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("Esta ocorrência não será lançada.");
    await userEvent.click(screen.getByRole("button", {name: "Voltar"}));
    expect(screen.queryByRole("alert")).toBeNull();
    await userEvent.click(screen.getByRole("button", {name: "Ignorar"}));
    await userEvent.click(screen.getByRole("button", {name: "Ignorar ocorrência"}));
    await waitFor(() => expect(skip).toHaveBeenCalledWith(expect.anything(), "luz", "2026-10-12", expect.any(String)));
  });

  it("shows the API's reason when confirming fails", async () => {
    vi.spyOn(finance, "confirmExpectedOccurrence").mockRejectedValue({isAxiosError: true, response: {status: 404, data: {status: 404, code: "not_found", title: "x"}}});
    setup();
    await userEvent.click(await screen.findByRole("button", {name: "Confirmar valor"}));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });
});

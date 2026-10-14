import "@testing-library/jest-dom/vitest";

import {screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it, vi} from "vitest";

import {ActionInboxView} from "@/components/finance/ActionInboxView";
import {renderWithQuery} from "@/components/finance/finance.test-utils";
import * as finance from "@/lib/api/finance";
import type {Bill, Recurrence, Verb} from "@/lib/api/financeTypes";

const verbs: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"];
const openBill: Bill = {
  id: "b1", direction: "payable", amount: 2800, account_id: "cash", category_id: "food", description: "iFood",
  competence_date: "2026-10-01", due_date: "2026-10-12", status: "forecast", origin: "manual", auto_settle: false, bucket: "overdue",
};
const blockedRecurrence: Recurrence = {
  id: "r1", direction: "payable", amount: 2800, account_id: "cash", category_id: "food", description: "Compra diária",
  expression: {kind: "daily"}, start: "2026-10-01", business_day_adjust: "none", auto_settle: false, archived: false, needs_attention: "card_archived",
};

afterEach(() => vi.restoreAllMocks());

describe("finance action inbox", () => {
  it("links overdue entries and blocked recurrences to their repair screens", async () => {
    vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false});
    vi.spyOn(finance, "listBills").mockImplementation(async (_ctx, direction) => ({data: direction === "payable" ? [openBill] : [], has_more: false}));
    vi.spyOn(finance, "listRecurrences").mockResolvedValue({data: [blockedRecurrence], has_more: false});
    vi.spyOn(finance, "listImports").mockResolvedValue({data: [{id: "i1", account_id: "cash", format: "ofx", created_at: "2026-10-10T00:00:00Z", lines: 4, duplicates: 0, rejected_count: 0, rejected: [], pending: 2}], has_more: false});
    vi.spyOn(finance, "listPendingOccurrences").mockResolvedValue({data: [], has_more: false});

    renderWithQuery(<ActionInboxView/>);

    expect(await screen.findByText("iFood")).toBeInTheDocument();
    expect(screen.getByRole("button", {name: "Abrir conta"})).toHaveAttribute("href", "/finance/bills?direction=payable&bill=b1");
    expect(screen.getByText("O cartão associado foi arquivado.")).toBeInTheDocument();
    expect(screen.getByRole("button", {name: "Corrigir"})).toHaveAttribute("href", "/finance/recurrences?recurrence=r1");
    expect(screen.getByText("2 lançamentos aguardando conciliação")).toBeInTheDocument();
    expect(screen.getByRole("button", {name: "Conciliar"})).toHaveAttribute("href", "/finance/import?account=cash");
  });

  it("confirms an expected recurring amount from the inbox", async () => {
    vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false});
    vi.spyOn(finance, "listBills").mockResolvedValue({data: [], has_more: false});
    vi.spyOn(finance, "listRecurrences").mockResolvedValue({data: [], has_more: false});
    vi.spyOn(finance, "listImports").mockResolvedValue({data: [], has_more: false});
    vi.spyOn(finance, "listPendingOccurrences").mockResolvedValue({data: [{recurrence_id:"food",nominal:"2026-10-12",due:"2026-10-12",direction:"payable",expected_amount:2800,account_id:"cash",category_id:"food",description:"iFood"}],has_more:false});
    const confirm = vi.spyOn(finance, "confirmExpectedOccurrence").mockResolvedValue({state:"confirmed",amount:2800,bill_id:"bill-1"});
    renderWithQuery(<ActionInboxView/>);
    expect(await screen.findByText("Previsto R$ 28,00 para 12/10/2026")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button",{name:"Confirmar valor"}));
    expect(confirm).toHaveBeenCalledWith(expect.anything(),"food","2026-10-12",2800,expect.any(String));
  });
});

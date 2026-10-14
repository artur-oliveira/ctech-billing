import {fireEvent, screen, waitFor} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import {renderWithQuery} from "@/components/finance/finance.test-utils";
import type {ExcessPayment} from "@/lib/api/consoleTypes";

import {ExcessPayments} from "./ExcessPayments";

const row: ExcessPayment = {id: "ex_1", invoice_id: "in_1", kind: "excess", amount: 10000, status: "open", paid_at: "2026-03-10T12:00:00Z", provider: "inter"};

describe("ExcessPayments", () => {
  it("tells the money is in the merchant's account and refunds once, after a confirmation", async () => {
    const refund = vi.fn().mockResolvedValue({...row, status: "refunding"});
    renderWithQuery(<ExcessPayments rows={[row]} currency="BRL" onRefund={refund}/>);
    expect(screen.getByText(/Recebemos R\$\s?100,00 a mais nesta fatura/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Devolver"}));
    expect(refund).not.toHaveBeenCalled();
    const confirm = await screen.findAllByRole("button", {name: "Devolver"});
    fireEvent.click(confirm[confirm.length - 1]);
    await waitFor(() => expect(refund).toHaveBeenCalledOnce());
    expect(await screen.findByText("Devolução em processamento")).toBeTruthy();
    expect(screen.queryByRole("button", {name: "Devolver"})).toBeNull();
  });

  it("offers to retry a credit refund the bank refused", () => {
    renderWithQuery(<ExcessPayments rows={[{...row, kind: "credit_refund", status: "refund_failed"}]} currency="BRL" onRefund={vi.fn()}/>);
    expect(screen.getByRole("button", {name: "Devolver"})).toBeTruthy();
  });

  it("renders nothing without rows", () => {
    const {container} = renderWithQuery(<ExcessPayments rows={[]} currency="BRL" onRefund={vi.fn()}/>);
    expect(container.textContent).toBe("");
  });
});

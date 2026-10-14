import {fireEvent, screen, waitFor} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import {renderWithQuery} from "@/components/finance/finance.test-utils";

import {CollectionSettings, percentToBasisPoints} from "./CollectionSettings";

describe("CollectionSettings", () => {
  it("reads Brazilian decimals into basis points", () => {
    expect(percentToBasisPoints("2,5")).toBe(250);
    expect(percentToBasisPoints("0,33")).toBe(33);
    expect(percentToBasisPoints("")).toBe(0);
    expect(percentToBasisPoints("abc")).toBeNaN();
  });

  it("sends percentages and refuses a fine above 20%", async () => {
    const submit = vi.fn().mockResolvedValue(undefined);
    renderWithQuery(<CollectionSettings initial={{fine_bp: 200, monthly_interest_bp: 100}} active={false} onSubmit={submit}/>);
    const fine = screen.getByLabelText("Multa por atraso (%)") as HTMLInputElement;
    expect(fine.value).toBe("2");
    fireEvent.change(fine, {target: {value: "25"}});
    fireEvent.click(screen.getByRole("button", {name: "Usar o Inter para cobrar"}));
    expect(await screen.findByText("Use um valor de 0 a 20.")).toBeTruthy();
    expect(submit).not.toHaveBeenCalled();
    fireEvent.change(fine, {target: {value: "2,5"}});
    fireEvent.click(screen.getByRole("button", {name: "Usar o Inter para cobrar"}));
    await waitFor(() => expect(submit).toHaveBeenCalledWith({late_fee_percent: 2.5, monthly_interest_percent: 1}));
  });

  it("says it is on, and saves under its own name once active", () => {
    renderWithQuery(<CollectionSettings initial={{fine_bp: 0, monthly_interest_bp: 0}} active onSubmit={vi.fn()}/>);
    expect(screen.getByText(/cobradas por Pix com vencimento/)).toBeTruthy();
    expect(screen.getByRole("button", {name: "Salvar"})).toBeTruthy();
  });
});

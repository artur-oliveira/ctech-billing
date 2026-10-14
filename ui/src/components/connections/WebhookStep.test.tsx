import {fireEvent, screen, waitFor} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import {renderWithQuery} from "@/components/finance/finance.test-utils";

import {WebhookStep} from "./WebhookStep";

describe("WebhookStep", () => {
  it("says confirmations take up to an hour without a notice", () => {
    renderWithQuery(<WebhookStep status="not_registered" onRegister={vi.fn()}/>);
    expect(screen.getByText("Sem webhook: as confirmações chegam em até uma hora.")).toBeTruthy();
  });

  it("never replaces another system's notice without the confirmation", async () => {
    const conflict = Object.assign(new Error("conflict"), {
      isAxiosError: true, response: {status: 409, data: {code: "byok_webhook_conflict", existing_host: "erp.example.com"}},
    });
    const register = vi.fn().mockRejectedValueOnce(conflict).mockResolvedValue(undefined);
    renderWithQuery(<WebhookStep status="not_registered" onRegister={register}/>);
    fireEvent.click(screen.getByRole("button", {name: "Registrar webhook novamente"}));
    await waitFor(() => expect(register).toHaveBeenCalledWith(false));
    expect(await screen.findByText(/vão para erp\.example\.com/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Substituir mesmo assim"}));
    expect(register).toHaveBeenCalledTimes(1);
    const confirm = await screen.findAllByRole("button", {name: "Substituir mesmo assim"});
    fireEvent.click(confirm[confirm.length - 1]);
    await waitFor(() => expect(register).toHaveBeenLastCalledWith(true));
  });
});

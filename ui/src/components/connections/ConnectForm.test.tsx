import {fireEvent, screen, waitFor} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import {renderWithQuery} from "@/components/finance/finance.test-utils";

import {ConnectForm} from "./ConnectForm";
import {fillValidForm} from "./testing";

describe("ConnectForm", () => {
  it("cannot be submitted without the authorization and both files", async () => {
    const submit = vi.fn();
    renderWithQuery(<ConnectForm onSubmit={submit}/>);
    fireEvent.click(screen.getByRole("button", {name: "Testar e conectar"}));
    expect(submit).not.toHaveBeenCalled();
    expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
  });

  it("never echoes the secret and clears the form after success", async () => {
    const submit = vi.fn().mockResolvedValue(undefined);
    renderWithQuery(<ConnectForm onSubmit={submit}/>);
    const secret = screen.getByLabelText("Client secret") as HTMLInputElement;
    expect(secret.type).toBe("password");
    expect(secret.autocomplete).toBe("off");
    fillValidForm();
    fireEvent.click(screen.getByRole("button", {name: "Testar e conectar"}));
    await waitFor(() => expect(submit).toHaveBeenCalledOnce());
    const sent = submit.mock.calls[0][0];
    expect(sent).toMatchObject({client_id: "cid-123", client_secret: "s3cr3t", account: "1234567", authorized: true, backfill_months: 3});
    await waitFor(() => expect((screen.getByLabelText("Client secret") as HTMLInputElement).value).toBe(""));
  });

  it("explains a key that does not match its certificate in words", async () => {
    const submit = vi.fn().mockRejectedValue({isAxiosError: true, response: {status: 422, data: {code: "byok_key_mismatch"}}});
    renderWithQuery(<ConnectForm onSubmit={submit}/>);
    fillValidForm();
    fireEvent.click(screen.getByRole("button", {name: "Testar e conectar"}));
    await waitFor(() => expect(screen.getByText("A chave não corresponde ao certificado. Envie o par baixado da mesma integração.")).toBeTruthy());
  });

  it("refuses an account with letters", () => {
    const submit = vi.fn();
    renderWithQuery(<ConnectForm onSubmit={submit}/>);
    fillValidForm();
    fireEvent.change(screen.getByLabelText("Conta corrente"), {target: {value: "12-34"}});
    fireEvent.click(screen.getByRole("button", {name: "Testar e conectar"}));
    expect(submit).not.toHaveBeenCalled();
    expect(screen.getByText("Use só números.")).toBeTruthy();
  });
});

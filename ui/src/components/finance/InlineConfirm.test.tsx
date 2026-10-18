import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {describe, expect, it, vi} from "vitest";

import {InlineConfirm} from "@/components/finance/InlineConfirm";

describe("InlineConfirm", () => {
  it("offers the safe answer and the consequential one, and shows the error as an alert", async () => {
    const onCancel = vi.fn(), onConfirm = vi.fn();
    render(<InlineConfirm text="Excluir?" cancelLabel="Manter" confirmLabel="Excluir" error="Falhou" onCancel={onCancel} onConfirm={onConfirm}/>);
    expect(screen.getByText("Excluir?")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Falhou");
    await userEvent.click(screen.getByRole("button", {name: "Manter"}));
    await userEvent.click(screen.getByRole("button", {name: "Excluir"}));
    expect(onCancel).toHaveBeenCalledOnce();
    expect(onConfirm).toHaveBeenCalledOnce();
  });
  it("disables the confirm while pending and draws no alert without an error", () => {
    render(<InlineConfirm text="?" cancelLabel="Não" confirmLabel="Sim" pending onCancel={() => {}} onConfirm={() => {}}/>);
    expect(screen.getByRole("button", {name: "Sim"})).toBeDisabled();
    expect(screen.queryByRole("alert")).toBeNull();
  });
  it("can be a retry offer (no safe answer) that announces itself", () => {
    render(<InlineConfirm announce tone="brand" text="Não terminou." confirmLabel="Tentar de novo" onConfirm={() => {}}/>);
    expect(screen.getByRole("alert")).toHaveTextContent("Não terminou.");
    expect(screen.getAllByRole("button")).toHaveLength(1);
  });
});

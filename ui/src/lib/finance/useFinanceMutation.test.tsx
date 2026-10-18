import {act, renderHook, waitFor} from "@testing-library/react";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import type {ReactNode} from "react";
import {toast} from "sonner";
import {afterEach, describe, expect, it, vi} from "vitest";

import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";

vi.mock("sonner", () => ({toast: {success: vi.fn(), error: vi.fn()}}));

const wrapper = ({children}: {children: ReactNode}) => (
  <QueryClientProvider client={new QueryClient({defaultOptions: {mutations: {retry: false}}})}>{children}</QueryClientProvider>
);

afterEach(() => vi.clearAllMocks());

describe("useFinanceMutation undo", () => {
  const undo = (run: () => Promise<unknown>) => ({message: "Feito.", actionLabel: "Desfazer", done: "Desfeito.", failed: "Não deu.", run});

  it("offers the undo after the write lands, and runs it with a key of its own", async () => {
    const run = vi.fn().mockResolvedValue(undefined);
    const {result} = renderHook(() => useFinanceMutation(async () => "ok", () => [], undefined, undefined, {undo: undo(run)}), {wrapper});
    await act(async () => { await result.current.mutateAsync(undefined as never); });
    expect(toast.success).toHaveBeenCalledWith("Feito.", expect.objectContaining({action: expect.objectContaining({label: "Desfazer"})}));
    const action = vi.mocked(toast.success).mock.calls[0][1]!.action as unknown as {onClick: () => void};
    action.onClick();
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Desfeito."));
    expect(run).toHaveBeenCalledWith(expect.anything(), undefined, "ok", expect.any(String));
  });

  it("says so when the undo itself fails, and offers nothing for a write that failed", async () => {
    const run = vi.fn().mockRejectedValue(new Error("x"));
    const {result} = renderHook(() => useFinanceMutation(async () => "ok", () => [], undefined, undefined, {undo: undo(run)}), {wrapper});
    await act(async () => { await result.current.mutateAsync(undefined as never); });
    (vi.mocked(toast.success).mock.calls[0][1]!.action as unknown as {onClick: () => void}).onClick();
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Não deu."));

    vi.clearAllMocks();
    const failing = renderHook(() => useFinanceMutation(async () => { throw new Error("no"); }, () => [], undefined, undefined, {undo: undo(run)}), {wrapper});
    await act(async () => { await failing.result.current.mutateAsync(undefined as never).catch(() => {}); });
    expect(toast.success).not.toHaveBeenCalled();
  });
});

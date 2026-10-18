import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen, waitFor} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import {FormError} from "@/components/finance/FormError";
import {renderWithQuery} from "@/components/finance/finance.test-utils";

const status = (code: number, detail = "") => ({response: {status: code, data: {status: code, detail}}});

afterEach(() => vi.restoreAllMocks());

describe("FormError", () => {
  it("draws nothing without an error or leftovers", () => {
    renderWithQuery(<FormError/>);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows the validation leftovers the form's controls did not claim", () => {
    renderWithQuery(<FormError error={status(422)} fe={{general: "Algo sobrou."}}/>);
    expect(screen.getByRole("alert")).toHaveTextContent("Algo sobrou.");
  });

  it("on a 409 says the row changed and refetches the whole space, whatever the form's own message", async () => {
    const client = new QueryClient();
    const spy = vi.spyOn(client, "invalidateQueries");
    render(<QueryClientProvider client={client}><FormError error={status(409)} fe={{general: "outra"}}/></QueryClientProvider>);
    expect(screen.getByRole("alert")).toHaveTextContent("Isto mudou enquanto você editava");
    await waitFor(() => expect(spy).toHaveBeenCalledOnce());
    expect(spy.mock.calls[0][0]!.queryKey).toEqual(expect.arrayContaining(["finance"]));
  });

  it("falls back to the error's own message when the form has no field leftovers to show", () => {
    renderWithQuery(<FormError error={status(500)}/>);
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });
});

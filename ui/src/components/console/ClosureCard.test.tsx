import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {cleanup, render, screen, waitFor} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import "@/lib/i18n";

const getClosure = vi.fn();
const getClosureBlockers = vi.fn();
const requestClosure = vi.fn();
vi.mock("@/lib/api/closure", async (original) => ({
  ...(await original<typeof import("@/lib/api/closure")>()),
  getClosure: (...a: unknown[]) => getClosure(...a),
  getClosureBlockers: (...a: unknown[]) => getClosureBlockers(...a),
  requestClosure: (...a: unknown[]) => requestClosure(...a),
}));
vi.mock("@/lib/console/scope", () => ({useConsoleScope: () => ({org: "org1", mode: "live"})}));

import {ClosureCard} from "./ClosureCard";

afterEach(cleanup);

function mount() {
  const qc = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return render(<QueryClientProvider client={qc}><ClosureCard/></QueryClientProvider>);
}

describe("ClosureCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getClosure.mockResolvedValue(null);
    getClosureBlockers.mockResolvedValue([]);
  });

  it("renders nothing for a non-owner (the status read is refused)", async () => {
    getClosure.mockRejectedValue({response: {status: 403, data: {code: "owner_required"}}});
    const {container} = mount();
    await waitFor(() => expect(getClosure).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("keeps the button disabled until the phrase matches, then asks and tells to check the e-mail", async () => {
    const user = userEvent.setup();
    requestClosure.mockResolvedValue({request_id: "r1", state: "awaiting_confirmation", confirm_by: "2026-10-18T12:00:00Z"});
    mount();
    const input = await screen.findByRole("textbox");
    const button = screen.getByRole("button", {name: /pedir encerramento/i});
    expect(button).toBeDisabled();
    await user.type(input, "ENCERRAR CONTA DE COBRANÇA");
    expect(button).toBeEnabled();
    await user.click(button);
    await waitFor(() => expect(requestClosure).toHaveBeenCalledWith({org: "org1", mode: "live"}, "ENCERRAR CONTA DE COBRANÇA"));
    expect(await screen.findByText(/link de confirmação/i)).toBeInTheDocument();
  });

  it("lists what blocks and disables the request", async () => {
    getClosureBlockers.mockResolvedValue([{code: "billing.tenant_active_subscriptions", count: 3}]);
    mount();
    expect(await screen.findByText(/3 assinatura\(s\)/i)).toBeInTheDocument();
    expect(screen.getByRole("button", {name: /pedir encerramento/i})).toBeDisabled();
  });

  it("shows where an open closure stands instead of the form", async () => {
    getClosure.mockResolvedValue({request_id: "r1", state: "closing", grace_until: "2026-10-24T12:00:00Z"});
    mount();
    expect(await screen.findByText(/Somente leitura/i)).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).toBeNull();
  });
});

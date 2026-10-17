import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {cleanup, render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it, vi} from "vitest";

import "@/lib/i18n";
import {ClosureLinkPage} from "./ClosureLinkPage";

afterEach(cleanup);

function mount(ui: React.ReactElement) {
  const qc = new QueryClient({defaultOptions: {mutations: {retry: false}}});
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("ClosureLinkPage", () => {
  it("does nothing until the button is pressed", async () => {
    const action = vi.fn().mockResolvedValue({request_id: "r1", state: "closing", grace_until: "2026-10-24T12:00:00Z"});
    const user = userEvent.setup();
    mount(<ClosureLinkPage kind="confirm" requestID="r1" token="t1" action={action}/>);
    expect(action).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", {name: /encerramento/i}));
    expect(action).toHaveBeenCalledWith("r1", "t1");
    expect(await screen.findByText(/Confirmado/i)).toBeInTheDocument();
  });

  it("offers no button for a link without parameters", () => {
    mount(<ClosureLinkPage kind="cancel" requestID="" token="" action={vi.fn()}/>);
    expect(screen.queryByRole("button", {name: /encerramento/i})).toBeNull();
    expect(screen.getByText(/inválido/i)).toBeInTheDocument();
  });

  it("says an invalid or too-late link in words, not in codes", async () => {
    const user = userEvent.setup();
    const action = vi.fn().mockRejectedValue({response: {status: 401, data: {code: "invalid_link"}}});
    mount(<ClosureLinkPage kind="cancel" requestID="r1" token="t1" action={action}/>);
    await user.click(screen.getByRole("button", {name: /encerramento/i}));
    expect(await screen.findByText(/inválido ou expirou/i)).toBeInTheDocument();
  });
});

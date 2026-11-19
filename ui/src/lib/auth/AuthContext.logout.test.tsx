import "@testing-library/jest-dom/vitest";

import {render, screen, waitFor} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {beforeEach, describe, expect, it, vi} from "vitest";

const order: string[] = [];
const mocks = vi.hoisted(() => ({
  removePush: vi.fn(),
  setAccessToken: vi.fn(),
  endSession: vi.fn(),
}));

vi.mock("@/lib/push/logout", () => ({removeThisDevicePush: mocks.removePush}));
vi.mock("@/lib/api/client", () => ({registerRefresh: vi.fn(), setAccessToken: mocks.setAccessToken}));
vi.mock("./oauth", () => ({
  decodeIdToken: vi.fn(),
  doRefresh: vi.fn().mockResolvedValue("token"),
  logout: mocks.endSession,
  startOAuthFlow: vi.fn(),
}));
vi.mock("./scopes", () => ({missingScopes: () => [], shouldUpgradeScopes: () => false}));

import {AuthProvider, useAuth} from "@/lib/auth/AuthContext";

function LogoutButton() {
  const {logout, loading} = useAuth();
  return <button disabled={loading} onClick={logout}>sair</button>;
}

describe("AuthProvider.logout", () => {
  beforeEach(() => {
    order.length = 0;
    mocks.removePush.mockReset().mockImplementation(async () => {
      order.push("push");
    });
    mocks.setAccessToken.mockReset().mockImplementation((v: string | null) => {
      if (v === null) order.push("token-dropped");
    });
    mocks.endSession.mockReset().mockImplementation(async () => {
      order.push("end-session");
    });
  });

  // The server row of this device is deleted with the access token, so the device must leave push before the token goes.
  it("removes this device from push BEFORE the access token is dropped, then ends the session", async () => {
    render(<AuthProvider><LogoutButton/></AuthProvider>);
    const button = await screen.findByRole("button", {name: "sair"});
    await waitFor(() => expect(button).toBeEnabled());
    await userEvent.click(button);
    await waitFor(() => expect(order).toEqual(["push", "token-dropped", "end-session"]));
  });
});

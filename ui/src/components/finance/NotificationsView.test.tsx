import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import type {ReactNode} from "react";
import {beforeEach, describe, expect, it, vi} from "vitest";

import type {PushPreferences} from "@/lib/api/push";

const api = vi.hoisted(() => ({
  getPushConfig: vi.fn(),
  getPushPreferences: vi.fn(),
  putPushPreferences: vi.fn(),
  sendPushTest: vi.fn(),
}));
vi.mock("@/lib/api/push", async importOriginal => ({...await importOriginal<typeof import("@/lib/api/push")>(), ...api}));

const toast = vi.hoisted(() => ({success: vi.fn(), error: vi.fn(), info: vi.fn()}));
vi.mock("sonner", () => ({toast}));

const push = vi.hoisted(() => ({
  status: "default" as string,
  subscribe: vi.fn(),
  unsubscribe: vi.fn(),
  canInstall: false,
  install: vi.fn(),
}));
vi.mock("@/lib/push/PushProvider", () => ({usePush: () => push}));

import {NotificationsView} from "@/components/finance/NotificationsView";

function wrapper({children}: {children: ReactNode}) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const enabled = {enabled: true, public_key: "PUB", vapid_kid: "k", max_subscriptions: 10};
const off: PushPreferences = {pending_reminder: {enabled: false, time: ""}};

describe("NotificationsView", () => {
  beforeEach(() => {
    Object.values(api).forEach(fn => fn.mockReset());
    Object.values(toast).forEach(fn => fn.mockReset());
    push.subscribe.mockReset().mockResolvedValue(undefined);
    push.unsubscribe.mockReset().mockResolvedValue(undefined);
    push.status = "default";
    push.canInstall = false;
    api.getPushConfig.mockResolvedValue(enabled);
    api.getPushPreferences.mockResolvedValue(off);
  });

  it("says so, and offers nothing, when push is off on this installation", async () => {
    api.getPushConfig.mockResolvedValue({enabled: false, max_subscriptions: 10});
    render(<NotificationsView/>, {wrapper});
    expect(await screen.findByText("As notificações não estão disponíveis agora.")).toBeInTheDocument();
    expect(screen.queryByRole("button", {name: "Ativar neste dispositivo"})).not.toBeInTheDocument();
  });

  it("asks for permission only when the button is clicked, never on load", async () => {
    render(<NotificationsView/>, {wrapper});
    const activate = await screen.findByRole("button", {name: "Ativar neste dispositivo"});
    expect(push.subscribe).not.toHaveBeenCalled();
    await userEvent.click(activate);
    expect(push.subscribe).toHaveBeenCalledOnce();
  });

  it("the reminder is offered only once this device is subscribed", async () => {
    render(<NotificationsView/>, {wrapper});
    await screen.findByRole("button", {name: "Ativar neste dispositivo"});
    expect(screen.queryByRole("switch", {name: "Lembrete de pendências"})).not.toBeInTheDocument();
  });

  it("explains a blocked permission instead of offering a button that cannot work", async () => {
    push.status = "denied";
    render(<NotificationsView/>, {wrapper});
    expect(await screen.findByText(/bloqueou as notificações/)).toBeInTheDocument();
    expect(screen.queryByRole("button", {name: "Ativar neste dispositivo"})).not.toBeInTheDocument();
  });

  it("on iPhone it says to add the app to the home screen first", async () => {
    push.status = "needs-install";
    render(<NotificationsView/>, {wrapper});
    expect(await screen.findByText(/Adicionar à Tela de Início/)).toBeInTheDocument();
  });

  describe("subscribed", () => {
    beforeEach(() => {
      push.status = "subscribed";
    });

    it("starts off, as the server holds it: the person picks the time", async () => {
      render(<NotificationsView/>, {wrapper});
      expect(await screen.findByRole("switch", {name: "Lembrete de pendências"})).not.toBeChecked();
      expect(screen.queryByLabelText("Horário do lembrete")).not.toBeInTheDocument();
    });

    // Only the personal space is counted (organization spaces are a documented ceiling): the copy must not promise more.
    it("says the reminder is about the personal space", async () => {
      render(<NotificationsView/>, {wrapper});
      await screen.findByRole("switch", {name: "Lembrete de pendências"});
      expect(screen.getByText(/espaço pessoal/)).toBeInTheDocument();
    });

    it("turning it on without a time shows the error and does not call the API", async () => {
      render(<NotificationsView/>, {wrapper});
      await userEvent.click(await screen.findByRole("switch", {name: "Lembrete de pendências"}));
      await userEvent.click(screen.getByRole("button", {name: "Salvar lembrete"}));
      expect(await screen.findByRole("alert")).toHaveTextContent("Escolha o horário do lembrete.");
      expect(api.putPushPreferences).not.toHaveBeenCalled();
    });

    // The time is a Select of the server's own 15-minute slots (00:00 to 23:45), not a free-typing native time input.
    it("offers the 96 slots of the day, every 15 minutes, as the only choices", async () => {
      render(<NotificationsView/>, {wrapper});
      await userEvent.click(await screen.findByRole("switch", {name: "Lembrete de pendências"}));
      expect(screen.queryByLabelText("Horário do lembrete", {selector: "input[type=time]"})).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("combobox", {name: "Horário do lembrete"}));
      const options = await screen.findAllByRole("option");
      expect(options).toHaveLength(96);
      expect(options[0]).toHaveTextContent("00:00");
      expect(options[1]).toHaveTextContent("00:15");
      expect(options[95]).toHaveTextContent("23:45");
    });

    it("saves the chosen time", async () => {
      api.putPushPreferences.mockResolvedValue({pending_reminder: {enabled: true, time: "08:00"}});
      render(<NotificationsView/>, {wrapper});
      await userEvent.click(await screen.findByRole("switch", {name: "Lembrete de pendências"}));
      await userEvent.click(screen.getByRole("combobox", {name: "Horário do lembrete"}));
      await userEvent.click(await screen.findByRole("option", {name: "08:00"}));
      await userEvent.click(screen.getByRole("button", {name: "Salvar lembrete"}));
      expect(api.putPushPreferences).toHaveBeenCalledWith({pending_reminder: {enabled: true, time: "08:00"}});
    });

    it("shows the time the server holds", async () => {
      api.getPushPreferences.mockResolvedValue({pending_reminder: {enabled: true, time: "20:30"}});
      render(<NotificationsView/>, {wrapper});
      expect(await screen.findByRole("combobox", {name: "Horário do lembrete"})).toHaveTextContent("20:30");
    });

    it("sends a test push and can turn this device off", async () => {
      api.sendPushTest.mockResolvedValue(undefined);
      render(<NotificationsView/>, {wrapper});
      await userEvent.click(await screen.findByRole("button", {name: "Enviar teste"}));
      expect(api.sendPushTest).toHaveBeenCalledOnce();
      await userEvent.click(screen.getByRole("button", {name: "Desativar neste dispositivo"}));
      expect(push.unsubscribe).toHaveBeenCalledOnce();
    });
  });
});

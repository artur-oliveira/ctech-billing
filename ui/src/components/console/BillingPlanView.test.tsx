import "@testing-library/jest-dom/vitest";

import {render, screen, waitFor, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import type {ReactNode} from "react";
import {afterEach, describe, expect, it, vi} from "vitest";

import {BillingPlanView} from "@/components/console/BillingPlanView";
import * as api from "@/lib/api/console";
import type {BillingPlanState} from "@/lib/api/consoleTypes";
import {ConsoleScopeProvider} from "@/lib/console/scope";

const ORG = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb";

const PLANS: BillingPlanState["plans"] = [
  {plan: "free", fee: 0, included: 50, overage_unit: 0, refuse: true, bank_connections: 1, users: 1, api_access: false},
  {plan: "basic", fee: 9990, included: 500, overage_unit: 25, refuse: false, bank_connections: 1, users: 3, api_access: false},
  {plan: "growth", fee: 29990, included: 2000, overage_unit: 18, refuse: false, bank_connections: 2, users: 10, api_access: true},
  {plan: "pro", fee: 69990, included: 5000, overage_unit: 15, refuse: false, bank_connections: 5, users: 25, api_access: true},
];

const state = (plan: string, quota: number, used = 47): BillingPlanState => ({
  current: {plan, subscription_id: "sub_1"},
  usage: {charges_this_month: used, quota, subscriptions_active: 12, subscriptions_quota: quota, bank_connections: 1, bank_connections_quota: 1},
  plans: PLANS,
});

function wrap(ui: ReactNode, mode: "live" | "test" = "live") {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return (
    <QueryClientProvider client={client}>
      <ConsoleScopeProvider value={{mode, org: ORG}}>{ui}</ConsoleScopeProvider>
    </QueryClientProvider>
  );
}

afterEach(() => vi.restoreAllMocks());

describe("BillingPlanView", () => {
  it("shows the usage and the four plans, the current one marked", async () => {
    vi.spyOn(api, "getBillingPlan").mockResolvedValue(state("free", 50));
    render(wrap(<BillingPlanView/>));
    expect(await screen.findByText("47 de 50 cobranças este mês")).toBeInTheDocument();
    expect(screen.getByText("12 de 50 assinaturas ativas")).toBeInTheDocument();
    const cards = screen.getAllByRole("article");
    expect(cards.map(c => within(c).getByRole("heading").textContent).sort()).toEqual(["Básico", "Crescimento", "Grátis", "Pro"]);
    const free = cards.find(c => within(c).queryByText("Plano atual"))!;
    expect(within(free).getByRole("heading")).toHaveTextContent("Grátis");
    expect(within(free).getByText("Acima do limite, novas cobranças são recusadas")).toBeInTheDocument();
    const basic = cards.find(c => within(c).getByRole("heading").textContent === "Básico")!;
    expect(within(basic).getByText("500 cobranças incluídas")).toBeInTheDocument();
    expect(within(basic).getByText(/R\$\s0,25 por cobrança adicional/)).toBeInTheDocument();
  });

  it("puts the current plan first for a phone, the catalogue order from lg up", async () => {
    vi.spyOn(api, "getBillingPlan").mockResolvedValue(state("growth", 2000));
    render(wrap(<BillingPlanView/>));
    const cards = await screen.findAllByRole("article");
    expect(within(cards[0]).getByRole("heading")).toHaveTextContent("Crescimento");
    expect(cards[0].className).toContain("lg:order-3");
  });

  it("changes plan after a confirmation", async () => {
    vi.spyOn(api, "getBillingPlan").mockResolvedValue(state("free", 50));
    const change = vi.spyOn(api, "changeBillingPlan").mockResolvedValue(state("growth", 2000));
    render(wrap(<BillingPlanView/>));
    await userEvent.click(await screen.findByRole("button", {name: "Mudar para Crescimento"}));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", {name: "Mudar para Crescimento"}));
    await waitFor(() => expect(change).toHaveBeenCalledWith({mode: "live", org: ORG}, "growth"));
  });

  it("says a downgrade below this month's use only stops new charges", async () => {
    vi.spyOn(api, "getBillingPlan").mockResolvedValue(state("basic", 500, 120));
    render(wrap(<BillingPlanView/>));
    await userEvent.click(await screen.findByRole("button", {name: "Mudar para Grátis"}));
    expect(await screen.findByText(/120 cobranças este mês/)).toBeInTheDocument();
  });

  it("changes nothing in test mode", async () => {
    vi.spyOn(api, "getBillingPlan").mockResolvedValue(state("free", 50));
    render(wrap(<BillingPlanView/>, "test"));
    await screen.findAllByRole("article");
    expect(screen.queryByRole("button", {name: /Mudar para/})).not.toBeInTheDocument();
    expect(screen.getByText(/O plano é mudado no modo de produção/)).toBeInTheDocument();
  });

  it("shows a change scheduled for the period end and keeps the current plan", async () => {
    const st = state("basic", 500);
    st.current = {...st.current, pending_plan: "free", pending_at: "2026-11-10"};
    vi.spyOn(api, "getBillingPlan").mockResolvedValue(st);
    const change = vi.spyOn(api, "changeBillingPlan").mockResolvedValue(state("basic", 500));
    render(wrap(<BillingPlanView/>));
    expect(await screen.findByText(/Muda para Grátis em 10\/11\/2026/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", {name: "Manter Básico"}));
    await waitFor(() => expect(change).toHaveBeenCalledWith({mode: "live", org: ORG}, "basic"));
  });

  it("says leaving a postpaid plan waits for the period end", async () => {
    vi.spyOn(api, "getBillingPlan").mockResolvedValue(state("basic", 500));
    render(wrap(<BillingPlanView/>));
    await userEvent.click(await screen.findByRole("button", {name: "Mudar para Pro"}));
    expect(await screen.findByText(/vale quando o período atual fechar/)).toBeInTheDocument();
  });
});

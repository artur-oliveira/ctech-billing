import "@testing-library/jest-dom/vitest";
import {screen, waitFor, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {lastWrite, renderWithQuery, serveFinanceMock} from "@/components/finance/finance.test-utils";
import {PlanView} from "@/components/finance/PlanView";
import {setFinanceScenario} from "@/dev/financeMockData";

vi.mock("next/navigation", () => ({useRouter: () => ({push: vi.fn()}), usePathname: () => "/finance/plans"}));
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => ({name: "Ana", authenticated: true, loading: false})}));

describe("PlanView", () => {
  let assign: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    window.localStorage.clear();
    setFinanceScenario("padrao"); // the override is module state: reset it between tests
    assign = vi.fn();
    vi.stubGlobal("location", {...window.location, assign});
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows the four plans, Free marked as current", async () => {
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    const cards = await screen.findAllByRole("article");
    expect(cards.map(c => within(c).getByRole("heading").textContent)).toEqual(["Free", "Basic", "Pro", "Sob demanda"]);
    expect(within(cards[0]).getByText("Seu plano")).toBeInTheDocument();
  });

  it("subscribes and sends the person to the checkout", async () => {
    const sent = serveFinanceMock();
    renderWithQuery(<PlanView/>);
    const basic = (await screen.findAllByRole("article"))[1];
    await userEvent.click(within(basic).getByRole("button", {name: /assinar/i}));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/checkout?token=mock"));
    expect(lastWrite(sent, "post", "/plan")).toMatchObject({price_ids: ["price_finance_basic_monthly"], name: "Ana"});
  });

  it("sends to the pending invoice on plan_payment_pending", async () => {
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    const cards = await screen.findAllByRole("article");
    await userEvent.click(within(cards[1]).getByRole("button", {name: /assinar/i}));
    await waitFor(() => expect(assign).toHaveBeenCalledTimes(1));
    await userEvent.click(within((await screen.findAllByRole("article"))[2]).getByRole("button", {name: /mudar/i}));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", {name: /pagar/i}));
    expect(assign).toHaveBeenLastCalledWith("/checkout?token=mock");
  });

  it("explains an over-limit plan without removing anything", async () => {
    setFinanceScenario("plan_over");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    expect(await screen.findByText(/Você tem 3 espaços e o plano Free permite 1\. Nada foi removido/)).toBeInTheDocument();
  });

  it("shows plans and no banner when usage is unavailable", async () => {
    setFinanceScenario("plan_no_usage");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    expect(await screen.findByText("Não foi possível ver o uso agora.")).toBeInTheDocument();
    expect(screen.getAllByRole("article")).toHaveLength(4);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("schedules Sob demanda from a paid Basic instead of charging now", async () => {
    setFinanceScenario("plan_basic");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    const ondemand = (await screen.findAllByRole("article"))[3];
    await userEvent.click(within(ondemand).getByRole("button", {name: /mudar/i}));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", {name: /confirmar/i}));
    expect(await screen.findByText(/Muda para Sob demanda em/)).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("suggests Basic when Sob demanda will cost more", async () => {
    setFinanceScenario("plan_ondemand");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    expect(await screen.findByText(/O Basic custa R\$\s?19,90/)).toBeInTheDocument();
  });

  // Review finding: with the first invoice open, paying it is THE action; no
  // plan card competes with it (one primary action, spec § 7).
  it("has one primary action when the first invoice is open", async () => {
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    const basic = (await screen.findAllByRole("article"))[1];
    await userEvent.click(within(basic).getByRole("button", {name: /assinar/i}));
    await screen.findByRole("button", {name: /pagar fatura/i});
    const primary = screen.getAllByRole("button").filter(b => b.className.includes("bg-brand-600"));
    expect(primary.map(b => b.textContent)).toEqual(["Pagar fatura"]);
  });
});

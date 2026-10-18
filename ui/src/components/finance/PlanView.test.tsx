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
    Element.prototype.scrollIntoView = vi.fn(); // jsdom has none
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
    expect(within(basic).getByText(/Faturas anteriores não mudam/)).toBeInTheDocument();
    await userEvent.click(within(basic).getByRole("button", {name: /ir para o pagamento/i}));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/checkout?token=mock"));
    expect(lastWrite(sent, "post", "/plan")).toMatchObject({price_ids: ["price_finance_basic_monthly"], name: "Ana"});
  });

  it("shows the owed invoice as the one action and blocks other changes", async () => {
    setFinanceScenario("plan_pending");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    await userEvent.click(await screen.findByRole("button", {name: /pagar fatura de R\$\s?19,90/i}));
    expect(assign).toHaveBeenCalledWith("/checkout?token=mock");
    expect(screen.getByText("Aguardando pagamento")).toBeInTheDocument();
    expect(screen.queryByRole("button", {name: /^mudar para/i})).not.toBeInTheDocument();
    const primary = screen.getAllByRole("button").filter(b => b.className.includes("bg-brand-600"));
    expect(primary).toHaveLength(1);
  });

  it("says when and how much: renewal date and price of the current plan", async () => {
    setFinanceScenario("plan_basic");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    expect(await screen.findByText("Próxima cobrança")).toBeInTheDocument();
    expect(screen.getAllByText(/R\$\s?19,90 por mês/).length).toBeGreaterThan(0);
  });

  it("says a plan ends, and what then", async () => {
    setFinanceScenario("plan_ends");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    expect(await screen.findByText("Termina no fim do período")).toBeInTheDocument();
    expect(screen.getByText(/você volta ao Free/i)).toBeInTheDocument();
  });

  it("marks a late renewal", async () => {
    setFinanceScenario("plan_pastdue");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    expect(await screen.findByText("Pagamento atrasado")).toBeInTheDocument();
  });

  it("confirms going back to Free in the card, then cancels", async () => {
    setFinanceScenario("plan_basic");
    const sent = serveFinanceMock();
    renderWithQuery(<PlanView/>);
    const free = (await screen.findAllByRole("article"))[0];
    await userEvent.click(within(free).getByRole("button", {name: /voltar ao free/i}));
    expect(sent.some(w => w.url.endsWith("/plan/cancel"))).toBe(false);
    await userEvent.click(within(free).getByRole("button", {name: /voltar ao free/i}));
    await waitFor(() => expect(sent.some(w => w.url.endsWith("/plan/cancel"))).toBe(true));
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
    await userEvent.click(within(ondemand).getByRole("button", {name: /agendar mudança/i}));
    expect(await screen.findByText(/Muda para Sob demanda em/)).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("suggests Basic when Sob demanda will cost more", async () => {
    setFinanceScenario("plan_ondemand");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    expect(await screen.findByText(/O Basic custa R\$\s?19,90/)).toBeInTheDocument();
  });

  // With the first invoice open, paying it is THE action; no plan card competes with it.
  it("has one primary action when the first invoice is open", async () => {
    setFinanceScenario("plan_pending");
    serveFinanceMock();
    renderWithQuery(<PlanView/>);
    await screen.findByRole("button", {name: /pagar fatura/i});
    const primary = screen.getAllByRole("button").filter(b => b.className.includes("bg-brand-600"));
    expect(primary).toHaveLength(1);
    expect(primary[0]).toHaveTextContent(/Pagar fatura de/);
  });
});

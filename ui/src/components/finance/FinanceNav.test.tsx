import "@testing-library/jest-dom/vitest";

import {render, screen, within} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

const push = vi.fn();
vi.mock("next/navigation", () => ({usePathname: () => "/finance/statement", useRouter: () => ({push})}));

import {FinanceNav} from "@/components/finance/FinanceNav";
import {FINANCE_MOCK_HOUSE} from "@/dev/financeMockData";
import {PERSONAL, setSpace} from "@/lib/console/space";

afterEach(() => {
  push.mockReset();
  setSpace(PERSONAL);
});

describe("FinanceNav", () => {
  it("lists the sections in the column, grouped, the current one marked", () => {
    render(<FinanceNav/>);
    const nav = screen.getByRole("navigation", {name: "Finanças"});
    const links = within(nav).getAllByRole("link");
    expect(links.map(l => l.textContent)).toEqual(["Resumo", "Pendências", "Agenda", "Extrato", "Importar", "Cartões", "Recorrências", "Relatórios", "Contas", "Plano"]);
    expect(within(nav).getByRole("link", {name: "Extrato"})).toHaveAttribute("aria-current", "page");
    expect(within(nav).getByRole("link", {name: "Resumo"})).not.toHaveAttribute("aria-current");
  });

  // The plan is the person's (plans spec D2): it lives in Pessoal only.
  it("has no Plano entry outside Pessoal", () => {
    setSpace({kind: "organization", organizationId: FINANCE_MOCK_HOUSE});
    render(<FinanceNav/>);
    const nav = screen.getByRole("navigation", {name: "Finanças"});
    expect(within(nav).queryByRole("link", {name: "Plano"})).toBeNull();
  });

  // BYOK (spec 2026-10-11 § 11): the bank connections live in an organization
  // space, for whoever may configure it (owner, admin).
  it("lists Conexões bancárias only in an organization space a person may configure", () => {
    setSpace({kind: "organization", organizationId: FINANCE_MOCK_HOUSE});
    const {unmount} = render(<FinanceNav canConfigure/>);
    let nav = screen.getByRole("navigation", {name: "Finanças"});
    expect(within(nav).getByRole("link", {name: "Conexões bancárias"})).toHaveAttribute("href", "/finance/settings/connections");
    unmount();
    render(<FinanceNav canConfigure={false}/>);
    nav = screen.getByRole("navigation", {name: "Finanças"});
    expect(within(nav).queryByRole("link", {name: "Conexões bancárias"})).toBeNull();
  });

  it("never lists Conexões bancárias in Pessoal", () => {
    render(<FinanceNav canConfigure/>);
    const nav = screen.getByRole("navigation", {name: "Finanças"});
    expect(within(nav).queryByRole("link", {name: "Conexões bancárias"})).toBeNull();
  });
});

import "@testing-library/jest-dom/vitest"

import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

const push = vi.fn()
vi.mock("next/navigation", () => ({usePathname: () => "/finance/statement", useRouter: () => ({push})}))

import {FinanceNav} from "@/components/finance/FinanceNav"
import {FINANCE_MOCK_HOUSE} from "@/dev/financeMockData"
import {PERSONAL, setSpace} from "@/lib/console/space"

afterEach(() => {
  push.mockReset()
  setSpace(PERSONAL)
})

describe("FinanceNav", () => {
  it("is one vertical list of the sections, the current one marked", () => {
    render(<FinanceNav/>)
    const nav = screen.getByRole("navigation", {name: "Finanças"})
    const links = within(nav).getAllByRole("link")
    expect(links.map(l => l.textContent)).toEqual(["Resumo", "Pendências", "Agenda", "Extrato", "Importar", "Cartões", "Recorrências", "Relatórios", "Contas", "Plano"])
    expect(within(nav).getByRole("link", {name: "Extrato"})).toHaveAttribute("aria-current", "page")
    expect(within(nav).getByRole("link", {name: "Resumo"})).not.toHaveAttribute("aria-current")
  })

  it("offers the same sections as a picker on a tablet", async () => {
    render(<FinanceNav/>)
    const picker = screen.getByRole("combobox", {name: "Seção"})
    expect(picker).toHaveTextContent("Extrato")
    await userEvent.click(picker)
    await userEvent.click(await screen.findByRole("option", {name: "Relatórios"}))
    expect(push).toHaveBeenCalledWith("/finance/reports")
  })

  // The plan is the person's (plans spec D2): it lives in Pessoal only.
  it("has no Plano entry outside Pessoal", () => {
    setSpace({kind: "organization", organizationId: FINANCE_MOCK_HOUSE})
    render(<FinanceNav/>)
    const nav = screen.getByRole("navigation", {name: "Finanças"})
    expect(within(nav).queryByRole("link", {name: "Plano"})).toBeNull()
  })
})

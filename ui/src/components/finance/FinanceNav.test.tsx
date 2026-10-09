import "@testing-library/jest-dom/vitest"

import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

const push = vi.fn()
vi.mock("next/navigation", () => ({usePathname: () => "/console/finance/statement", useRouter: () => ({push})}))

import {FinanceNav} from "@/components/finance/FinanceNav"

afterEach(() => push.mockReset())

describe("FinanceNav", () => {
  it("is one vertical list of the sections, the current one marked", () => {
    render(<FinanceNav/>)
    const nav = screen.getByRole("navigation", {name: "Finanças"})
    const links = within(nav).getAllByRole("link")
    expect(links.map(l => l.textContent)).toEqual(["Resumo", "Agenda", "Extrato", "Importar", "Cartões", "Recorrências", "Relatórios", "Contas"])
    expect(within(nav).getByRole("link", {name: "Extrato"})).toHaveAttribute("aria-current", "page")
    expect(within(nav).getByRole("link", {name: "Resumo"})).not.toHaveAttribute("aria-current")
  })

  it("offers the same sections as a picker on a tablet", async () => {
    render(<FinanceNav/>)
    const picker = screen.getByRole("combobox", {name: "Seção"})
    expect(picker).toHaveTextContent("Extrato")
    await userEvent.click(picker)
    await userEvent.click(await screen.findByRole("option", {name: "Relatórios"}))
    expect(push).toHaveBeenCalledWith("/console/finance/reports")
  })
})

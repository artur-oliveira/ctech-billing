import "@testing-library/jest-dom/vitest"

import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

const push = vi.fn()
vi.mock("next/navigation", () => ({usePathname: () => "/dashboard", useRouter: () => ({push})}))

import {SectionNav} from "@/components/navigation/SectionNav"

afterEach(() => push.mockReset())

const items = [
  {href: "/dashboard", label: "portal.nav.home", exact: true},
  {href: "/invoices", label: "portal.nav.invoices"},
  {href: "/subscriptions", label: "portal.nav.subscriptions"},
]

describe("SectionNav", () => {
  it("marks the current destination in the desktop list", () => {
    render(<SectionNav items={items} label="Portal" selectLabel="Seção"/>)
    const nav = screen.getByRole("navigation", {name: "Portal"})
    expect(within(nav).getByRole("link", {name: "Início"})).toHaveAttribute("aria-current", "page")
    expect(within(nav).getByRole("link", {name: "Faturas"})).not.toHaveAttribute("aria-current")
  })

  it("navigates to the selected section on tablet", async () => {
    render(<SectionNav items={items} label="Portal" selectLabel="Seção"/>)
    await userEvent.click(screen.getByRole("combobox", {name: "Seção"}))
    await userEvent.click(await screen.findByRole("option", {name: "Assinaturas"}))
    expect(push).toHaveBeenCalledWith("/subscriptions")
  })
})

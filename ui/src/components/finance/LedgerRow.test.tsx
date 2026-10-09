import "@testing-library/jest-dom/vitest"

import {act, fireEvent, render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {LedgerRow, type RowAction} from "@/components/finance/LedgerRow"

// UX batch 4: a phone list row reveals its secondary and destructive actions
// when swiped left, and always offers the same actions in a visible "⋯" menu
// (the gesture is never the only way). A laptop row is unchanged: the same
// actions are inline buttons.

function phone(matches: boolean) {
  window.matchMedia = vi.fn().mockImplementation((q: string) => ({
    matches: matches && q.includes("max-width"), media: q, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  })) as never
}

function rows(actions: (name: string) => RowAction[]) {
  return render(
    <ul>
      <LedgerRow title="Aluguel" amount="R$ 1.800,00" more={actions("Aluguel")}/>
      <LedgerRow title="Internet" amount="R$ 119,90" more={actions("Internet")}/>
    </ul>,
  )
}

const front = (name: string) => screen.getByText(name).closest("[data-swipe-front]") as HTMLElement
const strip = (name: string) => screen.getByText(name).closest("li")!.querySelector("[data-swipe-actions]") as HTMLElement

function drag(el: HTMLElement, dx: number, dy = 0) {
  fireEvent.pointerDown(el, {pointerId: 1, pointerType: "touch", clientX: 300, clientY: 100, isPrimary: true, button: 0})
  const steps = 6
  for (let i = 1; i <= steps; i++) fireEvent.pointerMove(el, {pointerId: 1, pointerType: "touch", clientX: 300 + (dx * i) / steps, clientY: 100 + (dy * i) / steps})
  fireEvent.pointerUp(el, {pointerId: 1, pointerType: "touch", clientX: 300 + dx, clientY: 100 + dy})
}

beforeEach(() => phone(true))
afterEach(() => vi.restoreAllMocks())

describe("LedgerRow's actions on a phone", () => {
  it("reveals the actions when swiped left past the threshold, and snaps back when not", () => {
    rows(() => [{key: "edit", label: "Editar", onSelect: vi.fn()}, {key: "del", label: "Excluir", destructive: true, onSelect: vi.fn()}])
    drag(front("Aluguel"), -20)
    expect(front("Aluguel")).toHaveAttribute("data-open", "false")
    expect(strip("Aluguel")).toHaveAttribute("inert")
    drag(front("Aluguel"), -160)
    expect(front("Aluguel")).toHaveAttribute("data-open", "true")
    expect(strip("Aluguel")).not.toHaveAttribute("inert")
    expect(within(strip("Aluguel")).getByRole("button", {name: "Excluir"})).toBeInTheDocument()
  })

  it("keeps one row open at a time", () => {
    rows(() => [{key: "del", label: "Excluir", destructive: true, onSelect: vi.fn()}])
    drag(front("Aluguel"), -160)
    drag(front("Internet"), -160)
    expect(front("Internet")).toHaveAttribute("data-open", "true")
    expect(front("Aluguel")).toHaveAttribute("data-open", "false")
  })

  it("leaves a vertical drag to the page: no reveal", () => {
    rows(() => [{key: "del", label: "Excluir", destructive: true, onSelect: vi.fn()}])
    drag(front("Aluguel"), -30, 120)
    expect(front("Aluguel")).toHaveAttribute("data-open", "false")
  })

  it("runs a revealed action (its confirmation is the row's) and closes", async () => {
    const del = vi.fn()
    rows(() => [{key: "del", label: "Excluir", destructive: true, onSelect: del}])
    drag(front("Aluguel"), -160)
    await userEvent.click(within(strip("Aluguel")).getByRole("button", {name: "Excluir"}))
    expect(del).toHaveBeenCalledTimes(1)
    expect(front("Aluguel")).toHaveAttribute("data-open", "false")
  })

  it("offers every action in a visible ⋯ menu, named for the row", async () => {
    const edit = vi.fn()
    rows(name => [{key: "edit", label: "Editar", onSelect: name === "Aluguel" ? edit : vi.fn()}, {key: "del", label: "Excluir", destructive: true, onSelect: vi.fn()}])
    await userEvent.click(screen.getByRole("button", {name: "Mais ações: Aluguel"}))
    const items = await screen.findAllByRole("menuitem")
    expect(items.map(i => i.textContent)).toEqual(["Editar", "Excluir"])
    await userEvent.click(items[0])
    expect(edit).toHaveBeenCalledTimes(1)
  })

  it("does not swipe on a laptop, where the actions are inline buttons", () => {
    phone(false)
    rows(() => [{key: "del", label: "Excluir", destructive: true, onSelect: vi.fn()}])
    drag(front("Aluguel"), -160)
    expect(front("Aluguel")).toHaveAttribute("data-open", "false")
  })

  it("closes an open row with Escape", () => {
    rows(() => [{key: "del", label: "Excluir", destructive: true, onSelect: vi.fn()}])
    drag(front("Aluguel"), -160)
    act(() => { fireEvent.keyDown(front("Aluguel"), {key: "Escape"}) })
    expect(front("Aluguel")).toHaveAttribute("data-open", "false")
  })
})

describe("LedgerRow's second figure", () => {
  it("shows a running balance beside the amount, named", () => {
    render(<ul><LedgerRow title="Pix mãe" amount="+R$ 210,00" balance="R$ 8.939,00" balanceLabel="Saldo"/></ul>)
    const li = screen.getByText("Pix mãe").closest("li")!
    // A phone line under the amount, and a laptop column (named for a screen reader).
    const named = within(li).getAllByText((_, el) => el?.textContent?.replace(/\s+/g, " ").trim() === "Saldo R$ 8.939,00" && el.children.length <= 1)
    expect(named).toHaveLength(2)
  })
})

import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {describe, expect, it, vi} from "vitest"

import {DateField} from "./DateField"

describe("DateField", () => {
  it("shows a civil date and gives one back, without a timezone shift", async () => {
    const change = vi.fn()
    render(<><label htmlFor="d">Vencimento</label><DateField id="d" value="2026-03-10" onValueChange={change}/></>)
    const trigger = screen.getByLabelText("Vencimento")
    expect(trigger).toHaveTextContent("10 de março de 2026")
    await userEvent.click(trigger)
    await userEvent.click(await screen.findByRole("button", {name: /^domingo, 15 de março de 2026$/}))
    expect(change).toHaveBeenCalledWith("2026-03-15")
  })

  // UX batch 4: an optional date can be emptied, by a visible button whose
  // name says which date it clears and contains its visible word.
  it("offers Limpar on an optional date with a value, and gives back an empty date", async () => {
    const change = vi.fn()
    const {rerender} = render(<DateField id="c" value="2026-12-10" onValueChange={change} clearLabel="Limpar data de término"/>)
    const clear = screen.getByRole("button", {name: "Limpar data de término"})
    expect(clear).toHaveTextContent("Limpar")
    await userEvent.click(clear)
    expect(change).toHaveBeenCalledWith("")
    rerender(<DateField id="c" value="" onValueChange={change} clearLabel="Limpar data de término"/>)
    expect(screen.queryByRole("button", {name: "Limpar data de término"})).not.toBeInTheDocument()
  })

  it("offers no Limpar on a required date", () => {
    render(<DateField id="r" value="2026-12-10" onValueChange={() => {}}/>)
    expect(screen.queryByRole("button", {name: /Limpar/})).not.toBeInTheDocument()
  })

  it("shows the placeholder when empty", () => {
    render(<DateField id="e" value="" onValueChange={() => {}} placeholder="Sem fim"/>)
    expect(screen.getByText("Sem fim")).toBeInTheDocument()
  })

  // Measured in the browser (scratchpad b5: 36px drawn, 44px hit); here the
  // contract that makes it so. Since @aoctech/ui 0.4 the trigger carries
  // `data-slot="date-picker-trigger"`, the key of its touch.css (UX batch 5;
  // batch 4 asked by class).
  it("is found by the touch rule (compact look, 44px target), inside the compact console too", () => {
    render(<div data-density="compact"><label htmlFor="t">Data</label><DateField id="t" value="" onValueChange={() => {}} className="w-40"/></div>)
    const trigger = screen.getByLabelText("Data")
    expect(trigger).toHaveAttribute("data-slot", "date-picker-trigger")
    expect(trigger).toHaveClass("w-40")
  })
})

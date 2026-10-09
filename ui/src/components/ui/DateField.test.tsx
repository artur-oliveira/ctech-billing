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

  it("shows the placeholder when empty", () => {
    render(<DateField id="e" value="" onValueChange={() => {}} placeholder="Sem fim"/>)
    expect(screen.getByText("Sem fim")).toBeInTheDocument()
  })

  // Measured in the browser by scratchpad audit.cjs; here the contract that
  // makes it so. The DatePicker's trigger has no data-slot for the touch rule.
  it("is a 44px target under touch, inside the compact console too", () => {
    render(<div data-density="compact"><label htmlFor="t">Data</label><DateField id="t" value="" onValueChange={() => {}} className="w-40"/></div>)
    const trigger = screen.getByLabelText("Data")
    expect(trigger).toHaveClass("touch:min-h-11")
    expect(trigger).toHaveClass("w-40")
  })
})

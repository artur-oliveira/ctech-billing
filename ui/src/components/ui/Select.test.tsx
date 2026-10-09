import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {useState} from "react"
import {describe, expect, it, vi} from "vitest"

import {Select} from "@/components/ui/Select"

const OPTIONS = [
  {value: "acc_01J9ZX", label: "Conta corrente"},
  {value: "acc_02K1AB", label: "Poupança"},
]

function Harness({initial = "", onChange = () => undefined}: {initial?: string; onChange?: (v: string) => void}) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <label htmlFor="acct">Conta</label>
      <Select id="acct" value={value} onValueChange={v => { setValue(v); onChange(v) }} options={OPTIONS} placeholder="Escolha…"/>
    </>
  )
}

describe("Select", () => {
  // The bug this component exists to avoid: a styled select that shows the
  // option's value ("acc_01J9ZX") instead of its label once one is chosen.
  it("shows the label of the selected option, never its value", () => {
    render(<Harness initial="acc_02K1AB"/>)
    const trigger = screen.getByRole("combobox", {name: "Conta"})
    expect(trigger).toHaveTextContent("Poupança")
    expect(trigger).not.toHaveTextContent("acc_02K1AB")
  })

  it("shows the placeholder until something is chosen", () => {
    render(<Harness/>)
    expect(screen.getByRole("combobox", {name: "Conta"})).toHaveTextContent("Escolha…")
  })

  it("selects by label and reports the value", async () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange}/>)
    await userEvent.click(screen.getByRole("combobox", {name: "Conta"}))
    await userEvent.click(await screen.findByRole("option", {name: "Conta corrente"}))
    expect(onChange).toHaveBeenCalledWith("acc_01J9ZX")
    expect(screen.getByRole("combobox", {name: "Conta"})).toHaveTextContent("Conta corrente")
    expect(screen.getByRole("combobox", {name: "Conta"})).not.toHaveTextContent("acc_01J9ZX")
  })
})

describe("Select actions", () => {
  function WithAction({onSelect, onChange = () => undefined}: {onSelect: () => void; onChange?: (v: string) => void}) {
    const [value, setValue] = useState("acc_01J9ZX")
    return (
      <Select aria-label="Conta" value={value} onValueChange={v => { setValue(v); onChange(v) }} options={OPTIONS}
        actions={[{label: "Nova conta", onSelect}]}/>
    )
  }

  // Base UI types ahead on a closed, focused trigger: "n" matches "Nova conta"
  // and would run it with no list ever on screen.
  it("never runs an action from typeahead on the closed trigger", async () => {
    const onSelect = vi.fn()
    const onChange = vi.fn()
    render(<WithAction onSelect={onSelect} onChange={onChange}/>)
    screen.getByRole("combobox", {name: "Conta"}).focus()
    await userEvent.keyboard("n")
    await userEvent.keyboard("p")
    expect(onSelect).not.toHaveBeenCalled()
    expect(screen.getByRole("combobox", {name: "Conta"})).not.toHaveTextContent("Nova conta")
  })

  it("runs it from a click in the open list", async () => {
    const onSelect = vi.fn()
    render(<WithAction onSelect={onSelect}/>)
    await userEvent.click(screen.getByRole("combobox", {name: "Conta"}))
    await userEvent.click(await screen.findByRole("option", {name: "Nova conta"}))
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(screen.getByRole("combobox", {name: "Conta"})).toHaveTextContent("Conta corrente")
  })

  it("runs it from Enter in the open list", async () => {
    const onSelect = vi.fn()
    render(<WithAction onSelect={onSelect}/>)
    await userEvent.click(screen.getByRole("combobox", {name: "Conta"}))
    const option = await screen.findByRole("option", {name: "Nova conta"})
    option.focus()
    await userEvent.keyboard("{Enter}")
    expect(onSelect).toHaveBeenCalledTimes(1)
  })
})

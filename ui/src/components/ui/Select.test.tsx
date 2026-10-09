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

  // UX batch 4: an optional choice can be undone. "Nenhuma" is an option like
  // any other, first in the list; choosing it gives back "", and the trigger
  // then names it rather than showing the placeholder.
  it("offers a none option first on an optional select, and empties the value", async () => {
    const change = vi.fn()
    function Optional() {
      const [v, setV] = useState("visa")
      return <><label htmlFor="o">Bandeira</label><Select id="o" value={v} none="Nenhuma" onValueChange={x => { change(x); setV(x) }}
        options={[{value: "visa", label: "Visa"}, {value: "elo", label: "Elo"}]}/></>
    }
    render(<Optional/>)
    const trigger = screen.getByRole("combobox", {name: "Bandeira"})
    await userEvent.click(trigger)
    const names = (await screen.findAllByRole("option")).map(o => o.textContent?.trim())
    expect(names).toEqual(["Nenhuma", "Visa", "Elo"])
    await userEvent.click(screen.getByRole("option", {name: "Nenhuma"}))
    expect(change).toHaveBeenCalledWith("")
    expect(trigger).toHaveTextContent("Nenhuma")
  })

  // UX batch 3: a card brand is picked by its mark AND its name. The mark is
  // decoration: the option's accessible name stays the label alone.
  it("shows an option's icon beside its label, in the list and on the trigger", async () => {
    const options = [
      {value: "visa", label: "Visa", icon: <svg data-testid="mark-visa"/>},
      {value: "elo", label: "Elo", icon: <svg data-testid="mark-elo"/>},
    ]
    function Brand() {
      const [v, setV] = useState("visa")
      return <><label htmlFor="b">Bandeira</label><Select id="b" value={v} onValueChange={setV} options={options}/></>
    }
    render(<Brand/>)
    const trigger = screen.getByRole("combobox", {name: "Bandeira"})
    expect(trigger).toHaveTextContent("Visa")
    expect(trigger.querySelector("[data-testid=mark-visa]")).not.toBeNull()
    await userEvent.click(trigger)
    const elo = await screen.findByRole("option", {name: "Elo"})
    expect(elo.querySelector("[data-testid=mark-elo]")?.closest("[aria-hidden]")).not.toBeNull()
    await userEvent.click(elo)
    expect(trigger.querySelector("[data-testid=mark-elo]")).not.toBeNull()
  })
})

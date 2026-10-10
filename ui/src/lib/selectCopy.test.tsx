import "@testing-library/jest-dom/vitest"

import {Select} from "@aoctech/ui"
import {act, render, screen} from "@testing-library/react"
import {afterEach, describe, expect, it} from "vitest"

import {changeAppLanguage} from "@/lib/i18n"
import {selectCopy} from "@/lib/selectCopy"

// UX batch 5: Select is @aoctech/ui's; billing keeps its own empty-choice copy
// ("Escolher…", not the catalogue's "Selecione…") and its language.
const OPTIONS = [{value: "acc_01J9ZX", label: "Conta corrente"}]

afterEach(async () => { await act(() => changeAppLanguage("pt-BR")) })

describe("selectCopy", () => {
  it("shows billing's placeholder in Portuguese", () => {
    render(<Select {...selectCopy()} aria-label="Conta" value="" onValueChange={() => {}} options={OPTIONS}/>)
    expect(screen.getByRole("combobox", {name: "Conta"})).toHaveTextContent("Escolher…")
  })

  it("follows the console's language", async () => {
    await act(() => changeAppLanguage("en"))
    render(<Select {...selectCopy()} aria-label="Account" value="" onValueChange={() => {}} options={OPTIONS}/>)
    expect(screen.getByRole("combobox", {name: "Account"})).toHaveTextContent("Choose…")
    expect(selectCopy().locale).toBe("en")
  })

  it("lets a call site's own placeholder win", () => {
    render(<Select {...selectCopy()} placeholder="Nenhuma" aria-label="Conta" value="" onValueChange={() => {}} options={OPTIONS}/>)
    expect(screen.getByRole("combobox", {name: "Conta"})).toHaveTextContent("Nenhuma")
  })
})

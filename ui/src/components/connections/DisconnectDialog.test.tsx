import {screen} from "@testing-library/react"
import {describe, expect, it, vi} from "vitest"

import {renderWithQuery} from "@/components/finance/finance.test-utils"

import {DisconnectDialog} from "./DisconnectDialog"

describe("DisconnectDialog", () => {
  it("names the open charges a disconnect would cancel", () => {
    renderWithQuery(<DisconnectDialog open onClose={vi.fn()} onConfirm={vi.fn()} loading={false} openCharges={3}/>)
    expect(screen.getByText("Há 3 cobranças em aberto. Desconectar cancela todas.")).toBeTruthy()
    expect(screen.getByRole("button", {name: "Cancelar cobranças e desconectar"})).toBeTruthy()
  })

  it("offers to disconnect anyway when the bank is down, and lists what to remove there", () => {
    const {unmount} = renderWithQuery(<DisconnectDialog open onClose={vi.fn()} onConfirm={vi.fn()} loading={false} unreachable/>)
    expect(screen.getByRole("button", {name: "Desconectar mesmo assim"})).toBeTruthy()
    unmount()
    renderWithQuery(<DisconnectDialog open onClose={vi.fn()} onConfirm={vi.fn()} loading={false}
      report={{charge_refs: ["ctb01A", "ctb01B"], webhook_url: "https://billing-byok-hooks.aoctech.app/w/inter/01H/"}}/>)
    expect(screen.getByText("Cobranças (txid): ctb01A, ctb01B")).toBeTruthy()
    expect(screen.queryByRole("button", {name: "Desconectar mesmo assim"})).toBeNull()
  })
})

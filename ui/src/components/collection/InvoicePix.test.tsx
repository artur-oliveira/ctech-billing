import {fireEvent, screen} from "@testing-library/react"
import {describe, expect, it, vi} from "vitest"

import {renderWithQuery} from "@/components/finance/finance.test-utils"
import type {InvoiceByok} from "@/lib/api/consoleTypes"

import {InvoicePix} from "./InvoicePix"

const open: InvoiceByok = {provider: "inter", attempt_status: "PENDING", payable: "00020126FAKEctb01", due_date: "2026-04-01",
  payable_until: "2026-05-01", can_retry: false, simulate: true, excess_payments: []}

describe("InvoicePix", () => {
  it("shows the QR, the code to copy, the due date and the last day to pay", () => {
    renderWithQuery(<InvoicePix byok={open} currency="BRL" onSimulate={vi.fn()} onNewPix={vi.fn()} busy={false}/>)
    expect(screen.getByTitle("QR Code do Pix desta fatura")).toBeTruthy()
    expect(screen.getByRole("button", {name: "Copiar código Pix"})).toBeTruthy()
    expect(screen.getByText("Pague até 01/05/2026")).toBeTruthy()
    expect(screen.getByText("Vence em 01/04/2026")).toBeTruthy()
  })

  it("offers the sandbox payment only in test mode", () => {
    const simulate = vi.fn()
    const {unmount} = renderWithQuery(<InvoicePix byok={open} currency="BRL" onSimulate={simulate} onNewPix={vi.fn()} busy={false}/>)
    fireEvent.click(screen.getByRole("button", {name: "Simular pagamento"}))
    expect(simulate).toHaveBeenCalledOnce()
    unmount()
    renderWithQuery(<InvoicePix byok={{...open, simulate: false}} currency="BRL" onSimulate={simulate} onNewPix={vi.fn()} busy={false}/>)
    expect(screen.queryByRole("button", {name: "Simular pagamento"})).toBeNull()
  })

  it("says in words why there is no Pix and offers a new one when it can", () => {
    const fresh = vi.fn()
    renderWithQuery(<InvoicePix byok={{can_retry: true, simulate: false, excess_payments: [], reason: "customer_tax_id_required"}}
      currency="BRL" onSimulate={vi.fn()} onNewPix={fresh} busy={false}/>)
    expect(screen.getByText("O cliente não tem CPF ou CNPJ cadastrado. Cadastre para gerar o Pix.")).toBeTruthy()
    fireEvent.click(screen.getByRole("button", {name: "Gerar novo Pix"}))
    expect(fresh).toHaveBeenCalledOnce()
  })
})

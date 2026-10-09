import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

import {CreditNoteDialog} from "@/components/console/CreditNoteDialog"
import * as consoleApi from "@/lib/api/console"
import type {ConsoleInvoice} from "@/lib/api/consoleTypes"

const INVOICE = {id: "inv_1", total: 10000, currency: "BRL"} as ConsoleInvoice

afterEach(() => vi.restoreAllMocks())

describe("CreditNoteDialog", () => {
  it("shows each validation error under its own field", async () => {
    vi.spyOn(consoleApi, "creditInvoice").mockRejectedValue({response: {status: 422, data: {status: 422, errors: [
      {field: "amount", code: "too_large", params: {max: 5000}},
      {field: "reason", code: "required"},
    ]}}})
    const client = new QueryClient({defaultOptions: {mutations: {retry: false}}})
    render(<QueryClientProvider client={client}><CreditNoteDialog open invoice={INVOICE} credited={0} onClose={() => {}} onIssued={() => {}}/></QueryClientProvider>)
    await userEvent.type(screen.getByLabelText(/^Valor/), "50,00")
    await userEvent.type(screen.getByLabelText(/^Motivo/), "erro")
    await userEvent.click(screen.getByRole("button", {name: /Emitir/}))
    expect(await screen.findByText("Obrigatório.")).toHaveAttribute("id", "credit-reason-error")
    expect(screen.getByLabelText(/^Valor/)).toHaveAccessibleDescription(/O valor máximo é/)
    expect(screen.getByLabelText(/^Motivo/)).toHaveAttribute("aria-invalid", "true")
    await userEvent.type(screen.getByLabelText(/^Motivo/), "x")
    expect(screen.queryByText("Obrigatório.")).not.toBeInTheDocument()
  })
})

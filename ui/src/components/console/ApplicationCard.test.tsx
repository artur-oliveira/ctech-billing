import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

import {ApplicationCard} from "@/components/console/ApplicationCard"
import * as api from "@/lib/api/console"
import type {MerchantApplication} from "@/lib/api/consoleTypes"
import {ConsoleScopeProvider} from "@/lib/console/scope"

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const
const APP: MerchantApplication = {
  id: "apl_1", status: "pending", organization_id: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", organization_name: "Padaria Bom Dia",
  company: {id: "cmp_1", legal_name: "Padaria Bom Dia LTDA", tax_id: "12345678000190"},
  description: "Assinaturas de pão para empresas do bairro", website: "https://padaria.example",
  expected_monthly_volume: "up_to_10k", contact_email: "c@padaria.example", applied_by: "usr_1", applied_at: "2026-10-09T14:20:00Z",
}

function show(app = APP) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <ConsoleScopeProvider value={SCOPE}><ApplicationCard application={app}/></ConsoleScopeProvider>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.restoreAllMocks())

describe("ApplicationCard", () => {
  it("shows an approved merchant's collection rail in words", () => {
    show({...APP, status: "approved", decided_at: "2026-10-10T10:00:00Z", payout: {rail: "byok", provider: "inter", status: "enabled"}})
    expect(screen.getByText("Pix com vencimento no Banco Inter · Ativa")).toBeInTheDocument()
  })

  it("shows what a reviewer decides on, with the volume's label, never its key", () => {
    show()
    expect(screen.getByText("Padaria Bom Dia")).toBeInTheDocument()
    expect(screen.getByText(/Padaria Bom Dia LTDA/)).toBeInTheDocument()
    expect(screen.getByText("Até R$ 10 mil")).toBeInTheDocument()
    expect(screen.queryByText("up_to_10k")).toBeNull()
  })

  it("approves only after the confirmation that says what approving does", async () => {
    const approve = vi.spyOn(api, "approveApplication").mockResolvedValue({...APP, status: "approved"})
    show()
    await userEvent.click(screen.getByRole("button", {name: "Aprovar"}))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText(/assinatura de acesso antecipado \(R\$ 0\)/)).toBeInTheDocument()
    expect(approve).not.toHaveBeenCalled()
    await userEvent.click(within(dialog).getByRole("button", {name: "Aprovar"}))
    await vi.waitFor(() => expect(approve).toHaveBeenCalledWith(SCOPE, "apl_1"))
  })

  it("will not reject without a reason of at least 10 characters", async () => {
    const reject = vi.spyOn(api, "rejectApplication").mockResolvedValue({...APP, status: "rejected"})
    show()
    await userEvent.click(screen.getByRole("button", {name: "Recusar"}))
    const dialog = await screen.findByRole("dialog")
    await userEvent.type(within(dialog).getByLabelText("Motivo"), "curto")
    await userEvent.click(within(dialog).getByRole("button", {name: "Recusar pedido"}))
    expect(reject).not.toHaveBeenCalled()
    await userEvent.type(within(dialog).getByLabelText("Motivo"), " demais, o site não abre")
    await userEvent.click(within(dialog).getByRole("button", {name: "Recusar pedido"}))
    await vi.waitFor(() => expect(reject).toHaveBeenCalledWith(SCOPE, "apl_1", "curto demais, o site não abre"))
  })

  it("has no actions on a decided application, and shows the reason of a rejected one", () => {
    show({...APP, status: "rejected", decided_at: "2026-10-10T09:00:00Z", rejection_reason: "O site não abre."})
    expect(screen.queryByRole("button", {name: "Aprovar"})).toBeNull()
    expect(screen.getByText("O site não abre.")).toBeInTheDocument()
  })
})

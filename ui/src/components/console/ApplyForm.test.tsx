import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it, vi} from "vitest"

vi.mock("next/navigation", () => ({useRouter: () => ({back: vi.fn(), push: vi.fn(), replace: vi.fn()})}))

import {ApplyForm} from "@/components/console/ApplyForm"
import * as api from "@/lib/api/console"
import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import type {ReactNode} from "react"
import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import {MerchantSelectionProvider} from "@/lib/console/selection"

const ORG = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"
const org = (merchant_status: MerchantOrganization["merchant_status"]): MerchantOrganization =>
  ({organization_id: ORG, selector: `org:${ORG}`, display_name: "Padaria Bom Dia", role: "owner", merchant_status})

function wrap(ui: ReactNode, status: MerchantOrganization["merchant_status"], mode: "live" | "test" = "live") {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return (
    <QueryClientProvider client={client}>
      <MerchantSelectionProvider value={{scope: {mode, org: ORG}, organization: org(status)}}>{ui}</MerchantSelectionProvider>
    </QueryClientProvider>
  )
}

afterEach(() => vi.restoreAllMocks())

const COMPANY = {company_id: "cmp_1", legal_name: "Padaria Bom Dia LTDA", tax_id: "12345678000190"}

async function fill(description: string) {
  await userEvent.click(await screen.findByRole("combobox", {name: "Empresa que cobra"}))
  await userEvent.click(await screen.findByRole("option", {name: /Padaria Bom Dia LTDA/}))
  await userEvent.type(screen.getByLabelText("O que você vende e para quem"), description)
  await userEvent.type(screen.getByLabelText("E-mail para contato"), "c@padaria.example")
}

describe("ApplyForm", () => {
  it("refuses a short description before any request, saying the minimum", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue({companies: [COMPANY], truncated: false})
    const apply = vi.spyOn(api, "applyForMerchant")
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    await fill("curta")
    await userEvent.click(screen.getByRole("button", {name: "Enviar pedido"}))
    expect(await screen.findByText("Use pelo menos 20 caracteres.")).toBeInTheDocument()
    expect(apply).not.toHaveBeenCalled()
  })

  it("sends the trimmed form and hands the application back", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue({companies: [COMPANY], truncated: false})
    const apply = vi.spyOn(api, "applyForMerchant").mockResolvedValue({id: "apl_1", status: "pending"} as never)
    const onApplied = vi.fn()
    render(wrap(<ApplyForm onApplied={onApplied}/>, "none"))
    await fill("Assinaturas de pão para empresas do bairro")
    await userEvent.click(screen.getByRole("button", {name: "Enviar pedido"}))
    await vi.waitFor(() => expect(onApplied).toHaveBeenCalled())
    expect(apply).toHaveBeenCalledWith({mode: "live", org: ORG}, {
      company_id: "cmp_1", description: "Assinaturas de pão para empresas do bairro", contact_email: "c@padaria.example",
    })
  })

  it("explains an organization with no company instead of an empty select", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue({companies: [], truncated: false})
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    expect(await screen.findByText(/ainda não tem empresa cadastrada/)).toBeInTheDocument()
    expect(screen.queryByRole("button", {name: "Enviar pedido"})).toBeNull()
  })

  it("offers a retry when the companies cannot be loaded", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockRejectedValue({response: {status: 503, data: {code: "companies_unavailable"}}})
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    expect(await screen.findByRole("button", {name: "Tentar de novo"})).toBeInTheDocument()
  })

  it("says when the list was cut and lets the company be named by its code", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue({companies: [COMPANY], truncated: true})
    const apply = vi.spyOn(api, "applyForMerchant").mockResolvedValue({id: "apl_1", status: "pending"} as never)
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    expect(await screen.findByText(/200 primeiras empresas/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", {name: "Informar o código da empresa"}))
    await userEvent.type(screen.getByLabelText("Código da empresa"), " cmp_far_down ")
    await userEvent.type(screen.getByLabelText("O que você vende e para quem"), "Assinaturas de pão para empresas do bairro")
    await userEvent.type(screen.getByLabelText("E-mail para contato"), "c@padaria.example")
    await userEvent.click(screen.getByRole("button", {name: "Enviar pedido"}))
    await vi.waitFor(() => expect(apply).toHaveBeenCalled())
    expect(apply.mock.calls[0][1]).toEqual(expect.objectContaining({company_id: "cmp_far_down"}))
  })

  it("shows the server's refusal of an organization that already bills", async () => {
    vi.spyOn(api, "listMerchantCompanies").mockResolvedValue({companies: [COMPANY], truncated: false})
    vi.spyOn(api, "applyForMerchant").mockRejectedValue({response: {status: 409, data: {code: "already_merchant"}}})
    render(wrap(<ApplyForm onApplied={() => {}}/>, "none"))
    await fill("Assinaturas de pão para empresas do bairro")
    await userEvent.click(screen.getByRole("button", {name: "Enviar pedido"}))
    expect(await screen.findByRole("alert")).toHaveTextContent(/já cobra pelo CTech Billing/)
  })
})

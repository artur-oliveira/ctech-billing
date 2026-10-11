import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, describe, expect, it} from "vitest"

import {MerchantInvitation} from "@/components/console/MerchantInvitation"
import {getMode} from "@/lib/console/mode"
import en from "@/locales/en"
import ptBR from "@/locales/pt-BR"
import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import type {ReactNode} from "react"
import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import {MerchantSelectionProvider} from "@/lib/console/selection"

const ORG = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"
const org = (merchant_status: MerchantOrganization["merchant_status"], application_status?: MerchantOrganization["application_status"]): MerchantOrganization =>
  ({organization_id: ORG, selector: `org:${ORG}`, display_name: "Padaria Bom Dia", role: "owner", merchant_status, application_status})

function wrap(ui: ReactNode, status: MerchantOrganization["merchant_status"], mode: "live" | "test" = "live",
  application?: MerchantOrganization["application_status"]) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return (
    <QueryClientProvider client={client}>
      <MerchantSelectionProvider value={{scope: {mode, org: ORG}, organization: org(status, application)}}>{ui}</MerchantSelectionProvider>
    </QueryClientProvider>
  )
}

afterEach(() => window.localStorage.clear())

describe("MerchantInvitation", () => {
  it("invites an organization that does not bill yet to apply", () => {
    render(wrap(<MerchantInvitation/>, "none"))
    expect(screen.getByRole("heading", {name: "Cobre seus clientes com o CTech Billing"})).toBeInTheDocument()
    expect(screen.getByRole("link", {name: "Quero cobrar meus clientes"})).toHaveAttribute("href", "/console/apply")
  })

  it("sends an applicant waiting for live mode to test mode", async () => {
    render(wrap(<MerchantInvitation/>, "test", "live", "pending"))
    expect(screen.getByText("Modo produção em análise")).toBeInTheDocument()
    expect(screen.queryByRole("link", {name: "Quero cobrar meus clientes"})).toBeNull()
    await userEvent.click(screen.getByRole("button", {name: "Ir para o modo de teste"}))
    expect(getMode()).toBe("test")
  })

  it("invites an organization CTech provisioned by hand (test tenant, never applied) to apply", () => {
    render(wrap(<MerchantInvitation/>, "test"))
    expect(screen.queryByText("Modo produção em análise")).toBeNull()
    expect(screen.getByRole("link", {name: "Quero cobrar meus clientes"})).toHaveAttribute("href", "/console/apply")
  })

  it("offers a rejected organization to apply again", () => {
    render(wrap(<MerchantInvitation/>, "rejected", "live", "rejected"))
    expect(screen.getByText("Modo produção não aprovado")).toBeInTheDocument()
    expect(screen.getByRole("link", {name: "Pedir de novo"})).toHaveAttribute("href", "/console/apply")
  })

  it("has no em dash in the console copy, in either language", () => {
    for (const catalog of [ptBR, en]) expect(JSON.stringify(catalog.console)).not.toContain("—")
  })
})

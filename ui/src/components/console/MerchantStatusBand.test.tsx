import "@testing-library/jest-dom/vitest"

import {render, screen} from "@testing-library/react"
import {afterEach, describe, expect, it, vi} from "vitest"

import {MerchantStatusBand} from "@/components/console/MerchantStatusBand"
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

describe("MerchantStatusBand", () => {
  it("says a suspended merchant's billing is suspended, in either mode", () => {
    for (const mode of ["live", "test"] as const) {
      const {unmount} = render(wrap(<MerchantStatusBand/>, "suspended", mode))
      expect(screen.getByText(/A cobrança de Padaria Bom Dia está suspensa/)).toBeInTheDocument()
      unmount()
    }
  })

  it("shows the rejection's reason and the way to apply again, in test mode", async () => {
    vi.spyOn(api, "getMerchantApplication").mockResolvedValue({status: "rejected", rejection_reason: "O site não abre."} as never)
    render(wrap(<MerchantStatusBand/>, "rejected", "test"))
    expect(await screen.findByText(/O modo produção não foi aprovado: O site não abre\./)).toBeInTheDocument()
    expect(screen.getByRole("link", {name: "Pedir de novo"})).toHaveAttribute("href", "/console/apply")
  })

  it("is silent for a live merchant and for one with no tenant in this mode", () => {
    const {container, unmount} = render(wrap(<MerchantStatusBand/>, "live"))
    expect(container).toBeEmptyDOMElement()
    unmount()
    const second = render(wrap(<MerchantStatusBand/>, "test", "live")) // the invitation says it there
    expect(second.container).toBeEmptyDOMElement()
  })
})

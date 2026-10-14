import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen} from "@testing-library/react"
import {afterEach, describe, expect, it, vi} from "vitest"

vi.mock("next/navigation", () => ({useSearchParams: () => new URLSearchParams("id=inv_mock_0042"), useRouter: () => ({push: vi.fn()})}))

import PortalInvoicePage from "@/app/(portal)/invoice/page"
import * as portal from "@/lib/api/portal"
import type {Invoice} from "@/lib/api/types"
import {mockAdapter, setScenario} from "@/dev/mockRuntime"

type Req = Parameters<typeof mockAdapter>[0]

async function fixture(): Promise<Invoice> {
  setScenario("vence_em_3_dias")
  const {data} = await mockAdapter({url: "/v1.0/portal/invoices/inv_mock_0042", method: "get", headers: {}} as unknown as Req)
  return data as Invoice
}

afterEach(() => vi.restoreAllMocks())

describe("/invoice", () => {
  it("captions the invoice by its number, never with the list's summary wording", async () => {
    const invoice = {...(await fixture()), number: "1042"}
    vi.spyOn(portal, "getInvoice").mockResolvedValue(invoice)
    render(<QueryClientProvider client={new QueryClient({defaultOptions: {queries: {retry: false}}})}><PortalInvoicePage/></QueryClientProvider>)
    expect(await screen.findByText("Fatura nº 1042")).toBeInTheDocument()
    expect(screen.queryByText(/\{\{/)).toBeNull()
    await vi.waitFor(() => expect(document.title).toContain("Fatura nº 1042"))
  })
})

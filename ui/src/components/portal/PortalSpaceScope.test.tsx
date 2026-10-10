import "@testing-library/jest-dom/vitest"

import {useQuery} from "@tanstack/react-query"
import {act, screen} from "@testing-library/react"
import type {AxiosRequestConfig} from "axios"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {renderWithQuery} from "@/components/finance/finance.test-utils"
import {PortalSpaceScope} from "@/components/portal/PortalSpaceScope"
import {apiClient} from "@/lib/api/client"
import {listInvoices, portalKeys} from "@/lib/api/portal"
import {setPortalSpace} from "@/lib/portal/space"

const ACME = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
vi.mock("sonner", () => ({toast: {info: vi.fn()}}))

beforeEach(() => {
  window.localStorage.clear()
  vi.spyOn(apiClient, "request").mockImplementation(async (config: AxiosRequestConfig) => {
    const who = (config.headers as Record<string, string>)["X-Billing-Space"]
    return {data: {data: [{id: who}], has_more: false}, status: 200, statusText: "OK", headers: {}, config} as never
  })
})
afterEach(() => vi.restoreAllMocks())

function Whose() {
  const {data} = useQuery({queryKey: portalKeys.invoiceList, queryFn: () => listInvoices()})
  return <p>{data ? `bills of ${data.data[0].id}` : "loading"}</p>
}

describe("PortalSpaceScope", () => {
  // Review finding: a change made anywhere but the switch (another tab, the
  // 404 fallback) must never leave one space's bills under another's name.
  it("never shows the previous space's bills after a change from outside the switch", async () => {
    renderWithQuery(<PortalSpaceScope><Whose/></PortalSpaceScope>)
    expect(await screen.findByText("bills of personal")).toBeInTheDocument()
    act(() => setPortalSpace({kind: "organization", organizationId: ACME}))
    expect(screen.queryByText("bills of personal")).toBeNull()
    expect(await screen.findByText(`bills of org:${ACME}`)).toBeInTheDocument()
  })

  it("keys every portal query by the space", () => {
    const personal = portalKeys.invoiceList
    setPortalSpace({kind: "organization", organizationId: ACME})
    expect(portalKeys.invoiceList).not.toEqual(personal)
    expect(portalKeys.invoice("i1")).toEqual(["portal", `org:${ACME}`, "invoices", "detail", "i1"])
  })
})

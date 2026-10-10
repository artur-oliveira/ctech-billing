import "@testing-library/jest-dom/vitest"

import type {AxiosRequestConfig} from "axios"
import {screen, waitFor} from "@testing-library/react"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {PortalSpaceSwitch} from "@/components/portal/PortalSpaceSwitch"
import {expectOptionsEventually, pick, renderWithQuery, selectByLabel} from "@/components/finance/finance.test-utils"
import {apiClient} from "@/lib/api/client"
import * as portal from "@/lib/api/portal"
import {getPortalSpace, setPortalSpace} from "@/lib/portal/space"

const ACME = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
const GONE = "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff"
const toast = vi.hoisted(() => ({info: vi.fn()}))
const push = vi.hoisted(() => vi.fn())
vi.mock("sonner", () => ({toast}))
vi.mock("next/navigation", () => ({useRouter: () => ({push})}))
vi.mock("@/lib/auth/AuthContext", () => ({useAuth: () => ({authenticated: true, loading: false})}))

const withAcme = {
  spaces: [
    {selector: "personal", display_name: "Pessoal"},
    {selector: `org:${ACME}`, display_name: "Acme LTDA", role: "admin"},
  ],
  organizations_unavailable: false,
}

let seen: AxiosRequestConfig[] = []
beforeEach(() => {
  window.localStorage.clear()
  toast.info.mockReset()
  push.mockReset()
  seen = []
  vi.spyOn(apiClient, "request").mockImplementation(async (config: AxiosRequestConfig) => {
    seen.push(config)
    return {data: {data: [], has_more: false}, status: 200, statusText: "OK", headers: {}, config} as never
  })
})
afterEach(() => vi.restoreAllMocks())

describe("PortalSpaceSwitch", () => {
  it("lists Pessoal first, then the organizations the server sent", async () => {
    vi.spyOn(portal, "listPortalSpaces").mockResolvedValue(withAcme)
    renderWithQuery(<PortalSpaceSwitch/>)
    await screen.findByRole("combobox", {name: "Conta"})
    await expectOptionsEventually("Conta", ["Pessoal", "Acme LTDA"])
    expect(selectByLabel("Conta")).toHaveTextContent("Pessoal")
  })

  it("switches to the organization, and the next portal call carries it", async () => {
    vi.spyOn(portal, "listPortalSpaces").mockResolvedValue(withAcme)
    renderWithQuery(<PortalSpaceSwitch/>)
    await screen.findByRole("combobox", {name: "Conta"})
    await expectOptionsEventually("Conta", ["Pessoal", "Acme LTDA"])
    await pick("Conta", "Acme LTDA")
    expect(getPortalSpace()).toEqual({kind: "organization", organizationId: ACME})
    await waitFor(() => expect(push).toHaveBeenCalledWith("/dashboard"))
    await portal.listInvoices()
    expect(seen.at(-1)?.headers).toMatchObject({"X-Billing-Space": `org:${ACME}`})
  })

  it("renders nothing for a person with no organization", async () => {
    const list = vi.spyOn(portal, "listPortalSpaces").mockResolvedValue({
      spaces: [{selector: "personal", display_name: "Pessoal"}], organizations_unavailable: false,
    })
    const {container} = renderWithQuery(<PortalSpaceSwitch/>)
    await waitFor(() => expect(list).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })

  it("falls back to Pessoal when the remembered organization is no longer listed", async () => {
    setPortalSpace({kind: "organization", organizationId: GONE})
    vi.spyOn(portal, "listPortalSpaces").mockResolvedValue(withAcme)
    renderWithQuery(<PortalSpaceSwitch/>)
    await waitFor(() => expect(getPortalSpace()).toEqual({kind: "personal"}))
    expect(toast.info).toHaveBeenCalledTimes(1)
  })

  it("keeps the remembered organization when the list could not be checked", async () => {
    setPortalSpace({kind: "organization", organizationId: ACME})
    const list = vi.spyOn(portal, "listPortalSpaces").mockResolvedValue({
      spaces: [{selector: "personal", display_name: "Pessoal"}], organizations_unavailable: true,
    })
    renderWithQuery(<PortalSpaceSwitch/>)
    await waitFor(() => expect(list).toHaveBeenCalled())
    expect(getPortalSpace()).toEqual({kind: "organization", organizationId: ACME})
  })
})

describe("a portal call for an organization that is gone", () => {
  it("falls back to Pessoal on 404 space-not-found", async () => {
    setPortalSpace({kind: "organization", organizationId: ACME})
    vi.mocked(apiClient.request).mockRejectedValueOnce({response: {status: 404, data: {type: "/problems/space-not-found"}}})
    await expect(portal.listInvoices()).rejects.toBeTruthy()
    expect(getPortalSpace()).toEqual({kind: "personal"})
    expect(toast.info).toHaveBeenCalledTimes(1)
  })
})

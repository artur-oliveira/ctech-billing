import "@testing-library/jest-dom/vitest"

import {waitFor} from "@testing-library/react"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {SpaceCreatedReturn} from "@/components/finance/SpaceCreatedReturn"
import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import {getSpace} from "@/lib/console/space"
import {SPACE_HANDOFF_STATE_KEY} from "@/lib/finance/spaceHandoff"

const WS = "0190a1b2-c3d4-7e5f-8a9b-cccccccccccc"
const FORGED = "0190a1b2-c3d4-7e5f-8a9b-dddddddddddd"
const ALL = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"] as const

const nav = vi.hoisted(() => ({replace: vi.fn(), params: new URLSearchParams()}))
vi.mock("next/navigation", () => ({
  useRouter: () => ({replace: nav.replace}),
  useSearchParams: () => nav.params,
}))

beforeEach(() => {
  window.localStorage.clear()
  window.sessionStorage.clear()
  nav.replace.mockReset()
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({
    spaces: [
      {selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: [...ALL], manage_people: false},
      {selector: `org:${WS}`, kind: "personal", display_name: "Casa", role: "owner", verbs: [...ALL], manage_people: true},
    ],
    organizations_unavailable: false,
  })
})
afterEach(() => vi.restoreAllMocks())

function returnWith(q: string, stored: string | null) {
  if (stored !== null) window.sessionStorage.setItem(SPACE_HANDOFF_STATE_KEY, stored)
  nav.params = new URLSearchParams(q)
  renderWithQuery(<SpaceCreatedReturn/>)
}

describe("SpaceCreatedReturn", () => {
  it("reloads the list and selects the new space when it is in it", async () => {
    returnWith(`organization_id=${WS}&state=s1`, "s1")
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/finance"))
    expect(finance.getFinanceSpaces).toHaveBeenCalled()
    expect(getSpace()).toEqual({kind: "organization", organizationId: WS})
  })

  // Spec § 9.5: the id on the return URL is never a selector by itself.
  it("selects nothing when the returned id is not in the server's list", async () => {
    returnWith(`organization_id=${FORGED}&state=s1`, "s1")
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/finance"))
    expect(finance.getFinanceSpaces).toHaveBeenCalled()
    expect(getSpace()).toEqual({kind: "personal"})
  })

  it("discards a return whose state differs, without asking the server", async () => {
    returnWith(`organization_id=${WS}&state=other`, "s1")
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/finance"))
    expect(getSpace()).toEqual({kind: "personal"})
    expect(finance.getFinanceSpaces).not.toHaveBeenCalled()
  })

  it("discards a return when no state was kept", async () => {
    returnWith(`organization_id=${WS}&state=s1`, null)
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/finance"))
    expect(getSpace()).toEqual({kind: "personal"})
    expect(finance.getFinanceSpaces).not.toHaveBeenCalled()
  })

  it("goes back to Finanças on a cancellation", async () => {
    returnWith("cancelled=1&state=s1", "s1")
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/finance"))
    expect(getSpace()).toEqual({kind: "personal"})
  })

  it("selects nothing, and still leaves, when the list cannot be read", async () => {
    vi.mocked(finance.getFinanceSpaces).mockRejectedValue(new Error("offline"))
    returnWith(`organization_id=${WS}&state=s1`, "s1")
    await waitFor(() => expect(nav.replace).toHaveBeenCalledWith("/finance"))
    expect(getSpace()).toEqual({kind: "personal"})
  })
})

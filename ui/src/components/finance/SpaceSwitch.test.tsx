import "@testing-library/jest-dom/vitest"

import {act, screen, waitFor} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {SpaceSwitch} from "@/components/finance/SpaceSwitch"
import {expectOptionsEventually, pick, renderWithQuery, selectByLabel} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"
import {getSpace, setSpace} from "@/lib/console/space"
import * as handoff from "@/lib/finance/spaceHandoff"

const ACME = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
const GONE = "0190a1b2-c3d4-7e5f-8a9b-ffffffffffff"
const CASA = "0190a1b2-c3d4-7e5f-8a9b-cccccccccccc"
const toast = vi.hoisted(() => ({info: vi.fn()}))
vi.mock("sonner", () => ({toast}))

const ALL = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"] as const

beforeEach(() => {
  window.localStorage.clear()
  toast.info.mockReset()
})
afterEach(() => vi.restoreAllMocks())

function serve(spaces: Awaited<ReturnType<typeof finance.getFinanceSpaces>>) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue(spaces)
}

describe("SpaceSwitch", () => {
  it("lists personal first and only what the server sent", async () => {
    serve({
      spaces: [
        {selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: [...ALL], manage_people: false},
        {selector: `org:${ACME}`, kind: "organization", display_name: "Acme LTDA", role: "admin", verbs: [...ALL], manage_people: false},
      ],
      organizations_unavailable: false,
    })
    renderWithQuery(<SpaceSwitch/>)
    await expectOptionsEventually("Espaço", ["Pessoal", "Acme LTDA"])
    // The trigger shows the space's NAME, never its "personal"/"org:…" value.
    expect(selectByLabel("Espaço")).toHaveTextContent("Pessoal")
    // No way to type an id: the control is a closed list.
    expect(screen.queryByRole("textbox")).toBeNull()
  })

  it("switches the space", async () => {
    serve({
      spaces: [
        {selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: [...ALL], manage_people: false},
        {selector: `org:${ACME}`, kind: "organization", display_name: "Acme LTDA", role: "member", verbs: ["finance.read"], manage_people: false},
      ],
      organizations_unavailable: false,
    })
    renderWithQuery(<SpaceSwitch/>)
    await expectOptionsEventually("Espaço", ["Pessoal", "Acme LTDA"])
    await pick("Espaço", "Acme LTDA")
    expect(getSpace()).toEqual({kind: "organization", organizationId: ACME})
    expect(selectByLabel("Espaço")).toHaveTextContent("Acme LTDA")
    expect(selectByLabel("Espaço")).not.toHaveTextContent(ACME)
  })

  it("keeps personal and says so when organizations are unavailable", async () => {
    serve({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: [...ALL], manage_people: false}], organizations_unavailable: true})
    renderWithQuery(<SpaceSwitch/>)
    expect(await screen.findByText("Outros espaços indisponíveis")).toBeInTheDocument()
  })

  // Review Focus 4.
  it("falls back to personal, once and with a notice, when the stored space is gone", async () => {
    act(() => setSpace({kind: "organization", organizationId: GONE}))
    serve({
      spaces: [
        {selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: [...ALL], manage_people: false},
        {selector: `org:${ACME}`, kind: "organization", display_name: "Acme LTDA", role: "admin", verbs: [...ALL], manage_people: false},
      ],
      organizations_unavailable: false,
    })
    renderWithQuery(<SpaceSwitch/>)
    await waitFor(() => expect(getSpace()).toEqual({kind: "personal"}))
    expect(toast.info).toHaveBeenCalledTimes(1)
  })

  it("names Pessoal in the reader's language, and other spaces by their own names", async () => {
    serve({
      spaces: [
        {selector: "personal", kind: "personal_default", display_name: "ignored", verbs: [...ALL], manage_people: false},
        {selector: `org:${CASA}`, kind: "personal", display_name: "Casa", role: "viewer", verbs: ["finance.read"], manage_people: false},
        {selector: `org:${ACME}`, kind: "organization", display_name: "Acme LTDA", role: "admin", verbs: [...ALL], manage_people: false},
      ],
      organizations_unavailable: false,
    })
    renderWithQuery(<SpaceSwitch/>)
    await expectOptionsEventually("Espaço", ["Pessoal", "Casa", "Acme LTDA"])
  })

  it("offers Gerenciar acesso only in a space whose people this person manages", async () => {
    serve({
      spaces: [
        {selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: [...ALL], manage_people: false},
        {selector: `org:${CASA}`, kind: "personal", display_name: "Casa", role: "owner", verbs: [...ALL], manage_people: true},
        {selector: `org:${ACME}`, kind: "organization", display_name: "Acme LTDA", role: "owner", verbs: [...ALL], manage_people: false},
      ],
      organizations_unavailable: false,
    })
    renderWithQuery(<SpaceSwitch/>)
    await expectOptionsEventually("Espaço", ["Pessoal", "Casa", "Acme LTDA"])
    expect(screen.queryByRole("link", {name: "Gerenciar acesso"})).toBeNull()
    await pick("Espaço", "Acme LTDA")
    expect(screen.queryByRole("link", {name: "Gerenciar acesso"})).toBeNull()
    await pick("Espaço", "Casa")
    const link = await screen.findByRole("link", {name: "Gerenciar acesso"})
    const u = new URL(link.getAttribute("href")!)
    expect(u.pathname).toBe("/account/spaces/people")
    expect(u.searchParams.get("id")).toBe(CASA)
  })

  it("starts the create handoff from Novo espaço", async () => {
    serve({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: [...ALL], manage_people: false}], organizations_unavailable: false})
    const start = vi.spyOn(handoff, "startCreateSpace").mockImplementation(() => {})
    renderWithQuery(<SpaceSwitch/>)
    await userEvent.click(await screen.findByRole("button", {name: "Novo espaço"}))
    expect(start).toHaveBeenCalledTimes(1)
  })
})

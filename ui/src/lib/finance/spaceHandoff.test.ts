import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {
  createSpaceURL, managePeopleURL, newHandoffState, readCreatedReturn, SPACE_CREATED_PATH, SPACE_HANDOFF_STATE_KEY, startCreateSpace,
} from "@/lib/finance/spaceHandoff"

const WS = "0190a1b2-c3d4-7e5f-8a9b-cccccccccccc"

beforeEach(() => window.sessionStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("newHandoffState", () => {
  it("is 128 random bits, hex", () => {
    const a = newHandoffState()
    expect(a).toMatch(/^[0-9a-f]{32}$/)
    expect(newHandoffState()).not.toBe(a)
  })
})

describe("createSpaceURL", () => {
  it("names the client, the return path on this origin and the state, and nothing else", () => {
    const u = new URL(createSpaceURL("https://billing.test", "s1"))
    expect(u.pathname).toBe("/account/spaces/new")
    expect(u.searchParams.get("return_to")).toBe(`https://billing.test${SPACE_CREATED_PATH}`)
    expect(u.searchParams.get("state")).toBe("s1")
    expect(u.searchParams.get("client_id")).toBeTruthy()
    expect([...u.searchParams.keys()].sort()).toEqual(["client_id", "return_to", "state"])
  })
})

describe("startCreateSpace", () => {
  it("keeps the state for the return leg before leaving", () => {
    const go = vi.fn()
    startCreateSpace(go, "https://billing.test")
    const stored = window.sessionStorage.getItem(SPACE_HANDOFF_STATE_KEY)
    expect(stored).toMatch(/^[0-9a-f]{32}$/)
    expect(go).toHaveBeenCalledTimes(1)
    expect(new URL(go.mock.calls[0][0]).searchParams.get("state")).toBe(stored)
  })
})

describe("managePeopleURL", () => {
  it("is ctech-account's ?id= people page, returning to Finanças", () => {
    const u = new URL(managePeopleURL(WS, "https://billing.test"))
    expect(u.pathname).toBe("/account/spaces/people")
    expect(u.searchParams.get("id")).toBe(WS)
    expect(u.searchParams.get("client_id")).toBeTruthy()
    expect(u.searchParams.get("return_to")).toBe("https://billing.test/finance")
  })
})

describe("readCreatedReturn", () => {
  const back = (q: string) => readCreatedReturn(new URLSearchParams(q))

  it("accepts a return that carries the state we sent", () => {
    window.sessionStorage.setItem(SPACE_HANDOFF_STATE_KEY, "abc")
    expect(back(`organization_id=${WS}&state=abc`)).toEqual({kind: "created", organizationId: WS})
  })

  it("is single-use: the same return read twice is discarded the second time", () => {
    window.sessionStorage.setItem(SPACE_HANDOFF_STATE_KEY, "abc")
    back(`organization_id=${WS}&state=abc`)
    expect(back(`organization_id=${WS}&state=abc`)).toEqual({kind: "discard"})
  })

  it("discards a state that differs, or a return when none was sent", () => {
    window.sessionStorage.setItem(SPACE_HANDOFF_STATE_KEY, "abc")
    expect(back(`organization_id=${WS}&state=abd`)).toEqual({kind: "discard"})
    expect(back(`organization_id=${WS}&state=abc`)).toEqual({kind: "discard"}) // cleared by the attempt
    expect(back(`organization_id=${WS}`)).toEqual({kind: "discard"})
  })

  it("reads a cancellation", () => {
    window.sessionStorage.setItem(SPACE_HANDOFF_STATE_KEY, "abc")
    expect(back("cancelled=1&state=abc")).toEqual({kind: "cancelled"})
  })

  it("discards an id that is not a canonical workspace id", () => {
    for (const id of ["USER#alice", WS.toUpperCase(), `${WS}#live`, ""]) {
      window.sessionStorage.setItem(SPACE_HANDOFF_STATE_KEY, "abc")
      expect(back(`organization_id=${encodeURIComponent(id)}&state=abc`)).toEqual({kind: "discard"})
    }
  })
})

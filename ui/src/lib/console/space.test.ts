import {afterEach, describe, expect, it, vi} from "vitest"

import {getSpace, parseSpace, PERSONAL, setSpace, spaceHeader} from "@/lib/console/space"

const ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

afterEach(() => {
  vi.restoreAllMocks()
  window.localStorage.clear()
})

describe("the space", () => {
  it("serialises to the server's selector syntax", () => {
    expect(spaceHeader(PERSONAL)).toBe("personal")
    expect(spaceHeader({kind: "organization", organizationId: ORG})).toBe(`org:${ORG}`)
  })

  it("reads anything that is not a canonical organization id as personal", () => {
    for (const raw of [null, "", "garbage", "org:", "org:   ", "USER#x", `org:${ORG.toUpperCase()}`, `org:${ORG}#live`, "personal:bob"]) {
      expect(parseSpace(raw)).toEqual(PERSONAL)
    }
    expect(parseSpace(`org:${ORG}`)).toEqual({kind: "organization", organizationId: ORG})
  })

  it("survives storage that throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked")
    })
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked")
    })
    expect(getSpace()).toEqual(PERSONAL)
    expect(() => setSpace({kind: "organization", organizationId: ORG})).not.toThrow()
  })

  it("keeps one snapshot object per stored value (useSyncExternalStore needs it)", () => {
    setSpace({kind: "organization", organizationId: ORG})
    expect(getSpace()).toBe(getSpace())
  })
})

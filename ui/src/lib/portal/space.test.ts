import {beforeEach, describe, expect, it} from "vitest"

import {getPortalSpace, setPortalSpace} from "./space"
import {getSpace, PERSONAL} from "@/lib/console/space"

const ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

describe("portal space", () => {
  beforeEach(() => window.localStorage.clear())

  it("defaults to personal and keeps a canonical organization", () => {
    expect(getPortalSpace()).toEqual(PERSONAL)
    setPortalSpace({kind: "organization", organizationId: ORG})
    expect(getPortalSpace()).toEqual({kind: "organization", organizationId: ORG})
  })

  it("never moves the finance selection", () => {
    setPortalSpace({kind: "organization", organizationId: ORG})
    expect(getSpace()).toEqual(PERSONAL)
  })
})

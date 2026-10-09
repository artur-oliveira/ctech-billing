import {describe, expect, it, vi} from "vitest"

import {isNoBillingAccount} from "./client"

// The portal answers 403 to four different callers and only one of them is a
// welcome. Getting this wrong in either direction is worse than the bug it
// replaces: a closed account greeted with "assine um plano" invites somebody
// who asked to be forgotten to come back, and a new reader shown a red block
// quoting "nenhuma conta de cobrança" is the failure this was written to fix.

const forbidden = (type: string) => ({
  response: {status: 403, data: {type, title: "Forbidden", status: 403}},
})

describe("isNoBillingAccount", () => {
  it("recognises the reader who has simply not bought anything yet", () => {
    expect(isNoBillingAccount(forbidden("/problems/no-billing-account"))).toBe(true)
  })

  it("leaves every other 403 looking like a refusal", () => {
    // A closed account (ADR 0009 — signing back in must not undo an erasure),
    // a machine token on a person's route, and a missing scope.
    for (const type of ["about:blank", "/problems/forbidden", "/problems/insufficient-scope"]) {
      expect(isNoBillingAccount(forbidden(type))).toBe(false)
    }
  })

  it("does not throw on the shapes that carry no problem document at all", () => {
    // A dropped connection has no response, and a proxy can return HTML.
    for (const error of [undefined, null, new Error("Network Error"), {response: {status: 502}}]) {
      expect(isNoBillingAccount(error)).toBe(false)
    }
  })
})

// Review Focus 5 (6.3b): an organization space answers 503 space-unavailable
// when ctech-account is down, while the personal space keeps working. Sending
// the whole app to /maintenance would take the working half down with it.
describe("the 503 rule", () => {
  async function reject503(data: unknown) {
    const {apiClient} = await import("./client")
    const replace = vi.fn()
    vi.stubGlobal("location", {...window.location, pathname: "/console/finance", search: "", replace})
    const handler = (apiClient.interceptors.response as unknown as {handlers: {rejected: (e: unknown) => Promise<unknown>}[]}).handlers[0].rejected
    await handler({response: {status: 503, data}, config: {}}).catch(() => undefined)
    vi.unstubAllGlobals()
    return replace
  }

  it("does not redirect to maintenance for space-unavailable", async () => {
    const replace = await reject503({type: "/problems/space-unavailable", status: 503})
    expect(replace).not.toHaveBeenCalled()
  })

  it("still redirects any other 503", async () => {
    const replace = await reject503({type: "about:blank", status: 503})
    expect(replace).toHaveBeenCalledWith(expect.stringMatching(/^\/maintenance\?from=/))
  })
})

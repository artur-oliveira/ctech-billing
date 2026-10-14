import type {InternalAxiosRequestConfig} from "axios"
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
    vi.stubGlobal("location", {...window.location, pathname: "/finance", search: "", replace})
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

// A hard reload fires queries before the boot-time silent refresh has answered.
// Sent without a token they come back 401 (and only recover through the retry);
// the first request with no token waits for that refresh instead.
describe("the first request without a token", () => {
  it("waits for the session to be resumed and carries its token", async () => {
    vi.resetModules()
    const client = await import("./client")
    const refresh = vi.fn(async () => {
      client.setAccessToken("resumed")
      return "resumed"
    })
    client.registerRefresh(refresh)
    const handler = (client.apiClient.interceptors.request as unknown as {handlers: {fulfilled: (c: unknown) => Promise<{headers: Record<string, string>}>}[]}).handlers[0].fulfilled
    const first = await handler({headers: {}})
    const second = await handler({headers: {}})
    expect(first.headers.Authorization).toBe("Bearer resumed")
    expect(second.headers.Authorization).toBe("Bearer resumed")
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it("asks once for a visitor who has no session, not on every request", async () => {
    vi.resetModules()
    const client = await import("./client")
    const refresh = vi.fn(async () => null)
    client.registerRefresh(refresh)
    const handler = (client.apiClient.interceptors.request as unknown as {handlers: {fulfilled: (c: unknown) => Promise<{headers: Record<string, string>}>}[]}).handlers[0].fulfilled
    await handler({headers: {}})
    const again = await handler({headers: {}})
    expect(again.headers.Authorization).toBeUndefined()
    expect(refresh).toHaveBeenCalledTimes(1)
  })
})

// Every write carries an Idempotency-Key (the API refuses one without): a
// screen that names its own intent key keeps it, any other gets a fresh one.
describe("the Idempotency-Key rule", () => {
  async function sent(method: string, headers: Record<string, string> = {}) {
    const {apiClient} = await import("./client")
    let seen: Record<string, unknown> = {}
    await apiClient.request({method, url: "/x", headers, adapter: async config => {
      seen = {...config.headers}
      return {data: null, status: 200, statusText: "OK", headers: {}, config}
    }})
    return seen["Idempotency-Key"]
  }

  it("adds a key to a write that has none", async () => {
    expect(await sent("post")).toEqual(expect.any(String))
    expect(await sent("delete")).toEqual(expect.any(String))
  })

  it("keeps the intent key a screen passed", async () => {
    expect(await sent("put", {"Idempotency-Key": "intent-1"})).toBe("intent-1")
  })

  it("adds none to a read", async () => {
    expect(await sent("get")).toBeUndefined()
  })
})

// The same key still running elsewhere (a double submit across tabs, a retry
// racing the original): ask again with the same key, and get its answer.
describe("a request still in progress", () => {
  it("is asked again with the same key until it answers", async () => {
    const {apiClient} = await import("./client")
    const keys: unknown[] = []
    const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
      keys.push(config.headers["Idempotency-Key"])
      if (keys.length < 3) {
        return Promise.reject(Object.assign(new Error("409"), {
          config, isAxiosError: true,
          response: {status: 409, data: {code: "idempotency_in_progress"}, headers: {"retry-after": "0"}, config},
        }))
      }
      return {data: "done", status: 201, statusText: "Created", headers: {}, config}
    })
    const res = await apiClient.request({method: "post", url: "/x", adapter})
    expect(res.data).toBe("done")
    expect(new Set(keys).size).toBe(1)
  })
})

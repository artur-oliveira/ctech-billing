import {readFileSync} from "node:fs"
import {resolve} from "node:path"

import {describe, expect, it} from "vitest"

import {missingScopes, OAUTH_SCOPE, shouldUpgradeScopes} from "./scopes"

interface ManifestScope {name: string; status: string; visibility: string}

const manifest: {scopes: ManifestScope[]} = JSON.parse(
  readFileSync(resolve(__dirname, "../../../../api/internal/oauthresource/scope-manifest.json"), "utf8"),
)
const published = new Set(manifest.scopes.filter(s => s.status === "active" && s.visibility === "public").map(s => s.name))
const requested = OAUTH_SCOPE.split(" ")

describe("OAUTH_SCOPE", () => {
  it("requests the finance scopes", () => {
    expect(requested).toEqual(expect.arrayContaining(["billing:finance:read", "billing:finance:write"]))
  })

  // ctech-account fails the whole sign-in on a scope the client was not granted,
  // so every billing scope asked for must be one the manifest publishes.
  it("asks only for public active billing scopes", () => {
    expect(requested.filter(s => s.startsWith("billing:") && !published.has(s))).toEqual([])
  })
})

function jwt(claims: object): string {
  const b64 = (s: string) => btoa(s).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_")
  return `${b64('{"alg":"RS256"}')}.${b64(JSON.stringify(claims))}.sig`
}

describe("missingScopes", () => {
  it("names what the token lacks of what the app asks for", () => {
    const old = requested.filter(s => !s.startsWith("billing:finance:")).join(" ")
    expect(missingScopes(jwt({scope: old}))).toEqual(["billing:finance:read", "billing:finance:write"])
  })
  it("is empty for a token granted everything, identity scopes aside", () => {
    expect(missingScopes(jwt({scope: requested.filter(s => s !== "openid" && s !== "profile").join(" ")}))).toEqual([])
  })
  it("never throws on a token it cannot read", () => {
    expect(missingScopes("not-a-jwt")).toEqual([])
  })
})

describe("shouldUpgradeScopes", () => {
  it("re-authorizes once, then waits before trying again", () => {
    expect(shouldUpgradeScopes(["billing:finance:read"], null, 1_000)).toBe(true)
    expect(shouldUpgradeScopes(["billing:finance:read"], 1_000, 1_000 + 60_000)).toBe(false)
    expect(shouldUpgradeScopes(["billing:finance:read"], 1_000, 1_000 + 11 * 60_000)).toBe(true)
    expect(shouldUpgradeScopes([], null, 1_000)).toBe(false)
  })
})

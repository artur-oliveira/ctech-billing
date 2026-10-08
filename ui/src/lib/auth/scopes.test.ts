import {readFileSync} from "node:fs"
import {resolve} from "node:path"

import {describe, expect, it} from "vitest"

import {OAUTH_SCOPE} from "./scopes"

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

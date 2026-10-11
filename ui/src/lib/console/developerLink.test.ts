import {describe, expect, it} from "vitest"

import {developerClientURL} from "@/lib/console/developerLink"

describe("developerClientURL", () => {
  it("opens ctech-account's new-client dialog with exactly these scopes", () => {
    const u = new URL(developerClientURL(["billing:invoices:read", "billing:usage:write"]))
    expect(u.pathname).toBe("/account/oauth-clients")
    expect(u.searchParams.get("new")).toBe("1")
    expect(u.searchParams.get("scopes")).toBe("billing:invoices:read,billing:usage:write")
  })
})

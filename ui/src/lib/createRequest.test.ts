import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {clearPendingCreate, dropCreateUnlessAt, requestCreate, takePendingCreate} from "@/lib/createRequest"

beforeEach(() => vi.useFakeTimers())
afterEach(() => {
  clearPendingCreate()
  vi.useRealTimers()
})

describe("createRequest", () => {
  it("is taken once, by its own kind only", () => {
    requestCreate("product")
    expect(takePendingCreate("webhook")).toBe(false)
    expect(takePendingCreate("product")).toBe(true)
    expect(takePendingCreate("product")).toBe(false)
  })

  it("is dropped after three seconds", () => {
    requestCreate("webhook", "/console/webhooks")
    vi.advanceTimersByTime(3_001)
    expect(takePendingCreate("webhook")).toBe(false)
  })

  it("is dropped when the person lands anywhere but its target", () => {
    requestCreate("product", "/console/catalog")
    dropCreateUnlessAt("/console/overview")
    expect(takePendingCreate("product")).toBe(false)
    requestCreate("product", "/console/catalog")
    dropCreateUnlessAt("/console/catalog")
    expect(takePendingCreate("product")).toBe(true)
  })
})

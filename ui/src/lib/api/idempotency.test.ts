import {describe, expect, it} from "vitest"

import {createIntent} from "@/lib/api/idempotency"

describe("an intent", () => {
  it("keeps its key across retries and changes it after success", () => {
    let n = 0
    const intent = createIntent(() => `k${++n}`)
    const first = intent.current()
    expect(intent.current()).toBe(first) // a retry after a timeout
    expect(intent.current()).toBe(first)
    intent.reset() // the submit succeeded
    expect(intent.current()).not.toBe(first)
  })

  it("never hands the same key to two intents", () => {
    let n = 0
    const gen = () => `k${++n}`
    expect(createIntent(gen).current()).not.toBe(createIntent(gen).current())
  })
})

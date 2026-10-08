"use client"

import {useRef} from "react"

/**
 * One Idempotency-Key per user intent.
 *
 * The key is created lazily and kept across retries of the same submit (a
 * timeout followed by "tentar de novo" is the same intent, and the server must
 * see the same key so it makes one bill, not two). It changes only after the
 * submit succeeded, because the next submit is a new intent.
 */
export interface Intent {
  current(): string
  reset(): void
}

export function createIntent(gen: () => string = () => crypto.randomUUID()): Intent {
  let key: string | null = null
  return {
    current: () => (key ??= gen()),
    reset: () => {
      key = null
    },
  }
}

/** The intent for one form or dialog; `done` after a successful submit. */
export function useIntent(): {key: () => string; done: () => void} {
  const ref = useRef<Intent | null>(null)
  ref.current ??= createIntent()
  const intent = ref.current
  return {key: () => intent.current(), done: () => intent.reset()}
}

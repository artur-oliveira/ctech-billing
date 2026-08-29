"use client"

/**
 * Test versus live, which is the one mode an operator switches (PRODUCT.md).
 *
 * It is a header on every console request (`X-Billing-Mode`), never a path
 * segment and never a field in a body: the server takes the tenant from the
 * signed-in owner and the mode from this header (ADR 0011), so a screen cannot
 * mix the two by forgetting to thread a parameter.
 *
 * Stored per browser rather than per session, because it is a working
 * preference — an operator who was in test mode yesterday is still building
 * something today. It is also, deliberately, always visible in the shell:
 * acting on the wrong mode is the expensive mistake.
 */
export type Mode = "live" | "test"

const KEY = "ctech-billing-console-mode"
const EVENT = "ctech-console-mode"

export function getMode(): Mode {
  if (typeof window === "undefined") return "live"
  try {
    return window.localStorage.getItem(KEY) === "test" ? "test" : "live"
  } catch {
    // A browser with site data blocked still gets a working console; what it
    // loses is the preference surviving a reload.
    return "live"
  }
}

export function setMode(mode: Mode) {
  try {
    window.localStorage.setItem(KEY, mode)
  } catch {
    // Ignored for the same reason: the switch must work even when nothing can
    // be remembered, because the alternative is a dead control.
  }
  window.dispatchEvent(new CustomEvent(EVENT, {detail: mode}))
}

/** Subscribes to changes, including the ones another tab makes. */
export function onModeChange(fn: (mode: Mode) => void): () => void {
  const local = () => fn(getMode())
  window.addEventListener(EVENT, local)
  window.addEventListener("storage", local)
  return () => {
    window.removeEventListener(EVENT, local)
    window.removeEventListener("storage", local)
  }
}

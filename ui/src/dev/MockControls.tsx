"use client"

import {useQueryClient} from "@tanstack/react-query"
import {useState, useSyncExternalStore} from "react"

import {currentScenario, setScenario} from "@/dev/mockRuntime"
import {DEFAULT_SCENARIO, MOCK_SCENARIOS, USE_MOCK, type MockScenario} from "@/lib/mockConfig"

/**
 * The scenario lives in localStorage, which the server cannot see.
 * `useSyncExternalStore` is what that shape is for: it takes a separate server
 * snapshot, so the markup React renders on the server and the markup it
 * hydrates agree instead of throwing a mismatch on every load.
 */
function useScenario(): MockScenario {
  return useSyncExternalStore(
    onChange => {
      window.addEventListener("ctech-mock-scenario", onChange)
      return () => window.removeEventListener("ctech-mock-scenario", onChange)
    },
    currentScenario,
    () => DEFAULT_SCENARIO
  )
}

/**
 * The scenario switcher, visible only in mock mode.
 *
 * It sits over the interface on purpose. Every state in the list is one this
 * portal has to render correctly and most of them cannot be reached against a
 * real backend without waiting days, so the ones that get designed are the
 * ones that are one click away.
 */
export function MockControls() {
  const queryClient = useQueryClient()
  const scenario = useScenario()
  const [open, setOpen] = useState(false)

  if (!USE_MOCK) return null

  function choose(next: MockScenario) {
    setScenario(next)
    setOpen(false)
    void queryClient.invalidateQueries()
  }

  return (
    <div className="fixed bottom-4 left-4 z-[60] print:hidden">
      {open && (
        <ul className="mb-2 w-64 overflow-hidden rounded-lg border border-border bg-background shadow-modal">
          {MOCK_SCENARIOS.map(s => (
            <li key={s.id}>
              <button
                type="button"
                onClick={() => choose(s.id)}
                className={`flex w-full items-center justify-between px-3 py-2 text-left text-sm transition-colors hover:bg-surface ${
                  s.id === scenario ? "font-medium text-brand-600" : "text-foreground"
                }`}
              >
                {s.label}
                {s.id === scenario && <span aria-hidden>•</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        aria-expanded={open}
        className="rounded-full border border-border bg-background px-3 py-1.5 font-mono text-xs text-muted-foreground shadow-card transition-colors hover:text-foreground"
      >
        mock · {scenario}
      </button>
    </div>
  )
}

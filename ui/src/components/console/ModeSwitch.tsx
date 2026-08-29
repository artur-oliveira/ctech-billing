"use client"

import {useQueryClient} from "@tanstack/react-query"

import {setMode} from "@/lib/console/mode"
import {useMode} from "@/lib/console/useMode"

const OPTIONS = [
  {id: "live", label: "Produção"},
  {id: "test", label: "Teste"},
] as const

/**
 * The one switch in this shell, and it is always visible.
 *
 * A segmented control rather than a dropdown: two options behind a chevron is a
 * menu that hides the answer to "which one am I in", which is the question this
 * control exists to answer before it exists to change anything.
 *
 * Switching does not invalidate anything, and it does not need to — the mode is
 * part of every console query key, so the two modes are two caches and the
 * screen re-reads the one it just moved to. What is removed instead is the
 * *other* mode's detail data, so a stale live invoice cannot be shown for a
 * frame while its test counterpart loads.
 */
export function ModeSwitch() {
  const mode = useMode()
  const queryClient = useQueryClient()

  return (
    <div
      role="group"
      aria-label="Modo"
      className="flex items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5"
    >
      {OPTIONS.map(option => {
        const active = mode === option.id
        return (
          <button
            key={option.id}
            type="button"
            aria-pressed={active}
            onClick={() => {
              if (active) return
              setMode(option.id)
              void queryClient.removeQueries({queryKey: ["console", mode]})
            }}
            className={`rounded-md px-2.5 py-1 text-xs transition-colors ${
              active
                ? option.id === "test"
                  ? "bg-warning text-background"
                  : "bg-brand-600 font-medium text-background"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
}

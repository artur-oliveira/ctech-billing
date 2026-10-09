"use client"

import {useQueryClient} from "@tanstack/react-query"
import {useTranslation} from "react-i18next"

import {setMode} from "@/lib/console/mode"
import {useMode} from "@/lib/console/useMode"

const OPTIONS = [
  {id: "live", label: "console.mode.live"},
  {id: "test", label: "console.mode.test"},
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
  const {t} = useTranslation()
  const mode = useMode()
  const queryClient = useQueryClient()

  return (
    <div
      role="group"
      aria-label={t("console.mode.label")}
      className="flex items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5"
    >
      {OPTIONS.map(option => {
        const active = mode === option.id
        return (
          <button
            key={option.id}
            type="button"
            // A segment like any other: under touch 32px drawn with a 44px target (globals.css); 24px on a desk.
            data-slot="segmented-item"
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
            {t(option.label)}
          </button>
        )
      })}
    </div>
  )
}

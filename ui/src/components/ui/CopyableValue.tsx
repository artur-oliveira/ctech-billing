"use client"

import {Copy} from "lucide-react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

/**
 * An identifier from another system (an external reference, an id): one line
 * in the code face, cut with an ellipsis and whole on hover, and a button that
 * copies it whole. Read to be pasted somewhere else, not to be read here.
 */
export function CopyableValue({value}: {value: string}) {
  const {t} = useTranslation()
  return (
    <span className="flex min-w-0 items-center gap-1">
      <span title={value} className="min-w-0 truncate font-mono text-xs">{value}</span>
      <button
        type="button"
        aria-label={t("common.copyValue", {value})}
        className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-surface hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 touch-target"
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(value)
            toast.success(t("common.copied"))
          } catch {
            toast.error(t("console.invoice.copyFailed"))
          }
        }}
      >
        <Copy aria-hidden className="size-3.5"/>
      </button>
    </span>
  )
}

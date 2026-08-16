"use client"

import {Button} from "@aoctech/ui"
import {RotateCw} from "lucide-react"

import {messageFor} from "@/lib/api/client"

/**
 * Scoped to a block, not to the page.
 *
 * P1 is composed from two independent requests. If the subscriptions call
 * fails, the reader should still see the invoice they owe — a page-level error
 * boundary would hide the one thing they came for because a different
 * request timed out.
 */
export function ErrorBlock({error, onRetry}: { error: unknown; onRetry?: () => void }) {
  return (
    <div
      role="alert"
      className="flex flex-col items-start gap-3 rounded-xl border border-border bg-surface px-5 py-4"
    >
      <p className="text-sm text-foreground">{messageFor(error)}</p>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RotateCw aria-hidden/>
          Tentar de novo
        </Button>
      )}
    </div>
  )
}

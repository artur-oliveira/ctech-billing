import {Button} from "@aoctech/ui";
import type {ReactNode} from "react";

interface InlineConfirmProps {
  /** The question, in the row's own words: what happens if they go on. */
  text: ReactNode
  /** Absent when there is no safe answer to offer (a retry of something already done). */
  cancelLabel?: string
  confirmLabel: string
  /** `danger` for anything that removes or stops; `brand` for a plain yes. */
  tone?: "danger" | "brand"
  pending?: boolean
  /** The mutation's error, already worded; rendered as an alert on its own line. */
  error?: ReactNode
  onCancel?: () => void
  onConfirm: () => void
  /** Announce it when it opens: for a question the person did not ask for (the server asked it). */
  announce?: boolean
  className?: string
}

/**
 * A confirmation (or, with no `cancelLabel`, a retry offer) that opens under the row it concerns (cancel a bill, end a
 * recurrence, pay a statement): the question, the safe answer first, the
 * consequential one last. Not a modal: the list stays visible behind it.
 */
export function InlineConfirm({text, cancelLabel, confirmLabel, tone = "danger", pending, error, onCancel, onConfirm, announce, className = "mt-3"}: InlineConfirmProps) {
  return (
    <div role={announce ? "alert" : undefined} className={`${className} flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in`}>
      <div className="min-w-0 flex-1 basis-56 text-foreground [overflow-wrap:anywhere]">{text}</div>
      <div className="flex gap-2">
        {cancelLabel && <Button size="sm" variant="outline" onClick={onCancel}>{cancelLabel}</Button>}
        <Button size="sm" variant={tone} disabled={pending} onClick={onConfirm}>{confirmLabel}</Button>
      </div>
      {error ? <p role="alert" className="w-full text-sm text-danger">{error}</p> : null}
    </div>
  );
}

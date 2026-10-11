import {cn} from "@aoctech/ui"
import type {TextareaHTMLAttributes} from "react"

/** A multi-line Input: the same border, focus ring, invalid state and density. */
export function Textarea({className, ...props}: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "min-h-28 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground outline-none",
        "placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/35",
        "disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-danger aria-invalid:ring-3 aria-invalid:ring-danger/20",
        "in-data-[density=compact]:px-2.5 in-data-[density=compact]:py-1.5",
        className,
      )}
      {...props}
    />
  )
}

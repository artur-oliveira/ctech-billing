import {cn} from "@aoctech/ui"

import type {Cents} from "@/lib/api/types"
import {money} from "@/lib/format"

/**
 * `data-numeric` switches on tabular figures (see globals.css). Proportional
 * digits make R$ 1.199,00 and R$ 89,90 impossible to compare down a column,
 * which is the one thing a list of invoices exists to let you do.
 */
export function Money({
                        cents,
                        currency = "BRL",
                        className,
                        size = "body",
                      }: {
  cents: Cents
  currency?: string
  className?: string
  /**
   * `hero` is the amount a whole screen is about, and on both screens that
   * have one it is the `h1` itself. `figure` is a headline amount inside a
   * block that is not the screen's subject.
   */
  size?: "body" | "figure" | "hero"
}) {
  return (
    <span
      data-numeric
      className={cn(
        "text-foreground",
        size === "hero" && "text-3xl font-semibold tracking-[-0.02em]",
        size === "figure" && "text-2xl font-semibold tracking-[-0.015em]",
        size === "body" && "font-medium",
        className
      )}
    >
      {money(cents, currency)}
    </span>
  )
}

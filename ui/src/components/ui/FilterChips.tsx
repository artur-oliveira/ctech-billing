"use client";

export interface ChipOption<V extends string> {
  value: V
  label: React.ReactNode
}

interface FilterChipsProps<V extends string> {
  /** Names the group for a screen reader. */
  label: string
  value: V
  onValueChange: (value: V) => void
  options: ChipOption<V>[]
  className?: string
}

/**
 * A one-of-many filter that is not limited to four options: the chips wrap onto
 * more lines instead of scrolling, so every option is visible at any width (a
 * segmented control with five or more segments clips on a phone). Each chip is a
 * toggle button (`aria-pressed`), in the tab order, 44px tall on a touch screen.
 *
 * Billing's own until @aoctech/ui has one (ctech-ui proposal: `ChipGroup`).
 */
export function FilterChips<V extends string>({label, value, onValueChange, options, className = ""}: FilterChipsProps<V>) {
  return (
    <div role="group" aria-label={label} className={`flex flex-wrap gap-1.5 ${className}`}>
      {options.map(o => {
        const on = o.value === value;
        return (
          <button key={o.value} type="button" aria-pressed={on} onClick={() => onValueChange(o.value)}
            className={`inline-flex min-h-11 items-center rounded-full border px-3 text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/60 sm:min-h-8 ${on ? "border-foreground bg-foreground font-medium text-background" : "border-border bg-surface text-muted-foreground hover:text-foreground"}`}>
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

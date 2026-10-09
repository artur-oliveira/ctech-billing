"use client"

import {cn} from "@aoctech/ui"
import {Select as SelectPrimitive} from "@base-ui/react/select"
import {Check, ChevronDown} from "lucide-react"

export interface SelectOption {
  value: string
  label: string
}

interface SelectProps {
  id?: string
  value: string
  onValueChange: (value: string) => void
  options: SelectOption[]
  placeholder?: string
  disabled?: boolean
  "aria-label"?: string
  "aria-invalid"?: boolean
  className?: string
}

/**
 * A styled select (the shadcn shape, on @base-ui/react — the primitives
 * @aoctech/ui already builds on), used instead of the native <select>.
 *
 * The trigger ALWAYS shows the selected option's label: the options are passed
 * to Root as `items` and the Value renders the label for the current value
 * explicitly, so an id such as "acc_01J9ZX" can never leak onto the screen —
 * the usual failure of a styled select wired in a hurry.
 *
 * Sized by the shell's density like every @aoctech/ui control (32px in the
 * compact console). Candidate for @aoctech/ui (ctech-ui has no Select yet).
 */
export function Select({id, value, onValueChange, options, placeholder = "Escolha…", disabled, className, ...aria}: SelectProps) {
  const labelOf = (v: string | null) => options.find(o => o.value === v)?.label
  return (
    <SelectPrimitive.Root
      items={options}
      value={value === "" ? null : value}
      onValueChange={v => onValueChange((v as string | null) ?? "")}
      disabled={disabled}
    >
      <SelectPrimitive.Trigger
        id={id}
        aria-label={aria["aria-label"]}
        aria-invalid={aria["aria-invalid"]}
        data-slot="select-trigger"
        className={cn(
          "flex h-11 w-full items-center justify-between gap-2 rounded-lg border border-border bg-background px-3 text-left text-sm text-foreground outline-none",
          "focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/35",
          "disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-danger aria-invalid:ring-3 aria-invalid:ring-danger/20",
          "in-data-[density=compact]:h-8 in-data-[density=compact]:px-2.5",
          className,
        )}
      >
        <SelectPrimitive.Value className="min-w-0 truncate data-[placeholder]:text-muted-foreground">
          {(v: string | null) => labelOf(v) ?? placeholder}
        </SelectPrimitive.Value>
        <SelectPrimitive.Icon className="shrink-0 text-muted-foreground">
          <ChevronDown aria-hidden className="size-4"/>
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Positioner sideOffset={4} alignItemWithTrigger={false} className="z-50 outline-none">
          <SelectPrimitive.Popup
            className={cn(
              "max-h-72 min-w-(--anchor-width) overflow-y-auto rounded-lg border border-border bg-background p-1 text-sm shadow-modal outline-none",
              "origin-(--transform-origin) transition-[opacity,transform] duration-150 ease-out data-[ending-style]:opacity-0 data-[starting-style]:scale-95 data-[starting-style]:opacity-0",
              "motion-reduce:transition-none",
            )}
          >
            <SelectPrimitive.List>
              {options.map(o => (
                <SelectPrimitive.Item
                  key={o.value}
                  value={o.value}
                  className="flex cursor-default items-center justify-between gap-3 rounded-md px-2 py-1.5 text-foreground outline-none select-none data-[highlighted]:bg-surface data-[selected]:font-medium"
                >
                  <SelectPrimitive.ItemText>{o.label}</SelectPrimitive.ItemText>
                  <SelectPrimitive.ItemIndicator className="text-brand-600">
                    <Check aria-hidden className="size-4"/>
                  </SelectPrimitive.ItemIndicator>
                </SelectPrimitive.Item>
              ))}
            </SelectPrimitive.List>
          </SelectPrimitive.Popup>
        </SelectPrimitive.Positioner>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  )
}

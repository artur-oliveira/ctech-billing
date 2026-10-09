"use client"

import {cn} from "@aoctech/ui"
import {Select as SelectPrimitive} from "@base-ui/react/select"
import {Check, ChevronDown} from "lucide-react"
import type {ReactNode} from "react"
import {useTranslation} from "react-i18next"

export interface SelectOption {
  value: string
  label: string
  /** Decoration beside the label (a card brand's mark), in the list and on the
   *  trigger. Hidden from assistive technology: the label is the name. */
  icon?: ReactNode
}

/**
 * Something to do rather than something to pick, listed after the options
 * behind a divider: "Novo espaço" at the end of the spaces. Choosing it runs
 * `onSelect` and leaves the value where it was.
 */
export interface SelectAction {
  label: string
  icon?: ReactNode
  onSelect: () => void
}

/** The values action items answer to. No option value starts with a NUL. */
const ACTION = "\u0000action:"

interface SelectProps {
  id?: string
  value: string
  onValueChange: (value: string) => void
  options: SelectOption[]
  actions?: SelectAction[]
  placeholder?: string
  disabled?: boolean
  "aria-label"?: string
  "aria-invalid"?: boolean
  "aria-describedby"?: string
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
export function Select({id, value, onValueChange, options, actions = [], placeholder, disabled, className, ...aria}: SelectProps) {
  const {t} = useTranslation()
  const emptyLabel = placeholder ?? t("auth.select.placeholder")
  const labelOf = (v: string | null) => options.find(o => o.value === v)?.label
  const iconOf = (v: string | null) => options.find(o => o.value === v)?.icon
  return (
    <SelectPrimitive.Root
      items={options}
      value={value === "" ? null : value}
      onValueChange={(v, details) => {
        const picked = (v as string | null) ?? ""
        if (picked.startsWith(ACTION)) {
          // Only a deliberate press in the open list (a click, or Enter) runs
          // an action. Base UI also types ahead on a closed, focused trigger,
          // and "n" there must not start "Novo espaço" and leave the page.
          if (details.reason === "item-press") actions[Number(picked.slice(ACTION.length))]?.onSelect()
          return
        }
        onValueChange(picked)
      }}
      disabled={disabled}
    >
      <SelectPrimitive.Trigger
        id={id}
        aria-label={aria["aria-label"]}
        aria-invalid={aria["aria-invalid"]}
        aria-describedby={aria["aria-describedby"]}
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
          {(v: string | null) => {
            const icon = iconOf(v)
            return icon ? <span className="flex min-w-0 items-center gap-2"><OptionIcon>{icon}</OptionIcon><span className="truncate">{labelOf(v)}</span></span> : labelOf(v) ?? emptyLabel
          }}
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
                  className="flex cursor-default items-center justify-between gap-3 rounded-md px-2 py-1.5 text-foreground outline-none select-none touch:min-h-11 data-[highlighted]:bg-surface data-[selected]:font-medium"
                >
                  <span className="flex min-w-0 items-center gap-2">
                    {o.icon && <OptionIcon>{o.icon}</OptionIcon>}
                    <SelectPrimitive.ItemText>{o.label}</SelectPrimitive.ItemText>
                  </span>
                  <SelectPrimitive.ItemIndicator className="text-brand-600">
                    <Check aria-hidden className="size-4"/>
                  </SelectPrimitive.ItemIndicator>
                </SelectPrimitive.Item>
              ))}
              {actions.length > 0 && <SelectPrimitive.Separator className="-mx-1 my-1 h-px bg-border"/>}
              {actions.map((a, i) => (
                <SelectPrimitive.Item
                  key={`${ACTION}${i}`}
                  value={`${ACTION}${i}`}
                  className="flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-foreground outline-none select-none touch:min-h-11 data-[highlighted]:bg-surface"
                >
                  {a.icon && <span aria-hidden className="flex size-4 shrink-0 items-center justify-center text-muted-foreground [&_svg]:size-4">{a.icon}</span>}
                  <SelectPrimitive.ItemText>{a.label}</SelectPrimitive.ItemText>
                </SelectPrimitive.Item>
              ))}
            </SelectPrimitive.List>
          </SelectPrimitive.Popup>
        </SelectPrimitive.Positioner>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  )
}

function OptionIcon({children}: {children: ReactNode}) {
  return <span aria-hidden className="flex shrink-0 items-center">{children}</span>
}

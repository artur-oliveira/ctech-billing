"use client"

import {Button, cn} from "@aoctech/ui"
import {Menu} from "@base-ui/react/menu"
import {Ellipsis} from "lucide-react"

export interface RowMenuItem {
  key: string
  label: string
  onSelect: () => void
  destructive?: boolean
}

/**
 * A row's "⋯": the visible, keyboard and screen-reader way to every action a
 * swipe reveals (UX batch 4), so a gesture is never the only way in. Named for
 * the row ("Mais ações: Aluguel"). Candidate for @aoctech/ui, with the swipe.
 */
export function RowMenu({label, items, className}: {label: string; items: RowMenuItem[]; className?: string}) {
  return (
    <Menu.Root>
      <Menu.Trigger render={<Button variant="ghost" size="icon" aria-label={label} className={cn("min-w-9 text-muted-foreground", className)}/>}>
        <Ellipsis aria-hidden className="size-4"/>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner sideOffset={4} align="end" className="z-50 outline-none">
          <Menu.Popup
            className={cn(
              "min-w-44 rounded-lg border border-border bg-background p-1 text-sm shadow-modal outline-none",
              "origin-(--transform-origin) transition-[opacity,transform] duration-150 ease-out data-[ending-style]:opacity-0 data-[starting-style]:scale-95 data-[starting-style]:opacity-0",
              "motion-reduce:transition-none",
            )}
          >
            {items.map(i => (
              <Menu.Item
                key={i.key}
                onClick={i.onSelect}
                className={cn(
                  "flex min-h-11 cursor-default items-center rounded-md px-3 outline-none select-none data-[highlighted]:bg-surface sm:min-h-9",
                  i.destructive ? "text-danger" : "text-foreground",
                )}
              >
                {i.label}
              </Menu.Item>
            ))}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  )
}

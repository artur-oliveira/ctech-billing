"use client";

import {useEffect, useRef} from "react";

import {segmentHit} from "@/components/ui/segmentHit";

export interface TabOption<V extends string> {
  value: V
  label: React.ReactNode
}

interface TabsProps<V extends string> {
  /** Names the tab list for a screen reader. */
  label: string
  value: V
  onValueChange: (value: V) => void
  options: TabOption<V>[]
  /** Ids derive from it: the tab is `tabId(idBase, value)`, the panel it controls `panelId(idBase)`. */
  idBase: string
  className?: string
}

export const tabId = (idBase: string, value: string) => `${idBase}-tab-${value}`;
export const panelId = (idBase: string) => `${idBase}-panel`;

/**
 * Tabs that change what the page shows (a segmented control is for a view of the
 * same thing). The WAI-ARIA pattern: one tab in the tab order (roving tabindex),
 * Left/Right/Home/End move and select, the panel is `role="tabpanel"` with the
 * ids from `tabId`/`panelId`. One line at every width: on a narrow screen the
 * list scrolls inside its own box and keeps the selected tab in view, never the
 * page, because a wrapped row 2px apart would share its 44px targets.
 */
export function Tabs<V extends string>({label, value, onValueChange, options, idBase, className = ""}: TabsProps<V>) {
  const refs = useRef(new Map<string, HTMLButtonElement>());
  useEffect(() => {
    // Not in jsdom; and "nearest" never moves a tab that is already visible.
    refs.current.get(value)?.scrollIntoView?.({inline: "nearest", block: "nearest"});
  }, [value]);

  const onKeyDown = (e: React.KeyboardEvent) => {
    const i = options.findIndex(o => o.value === value);
    const to = e.key === "ArrowRight" ? (i + 1) % options.length
      : e.key === "ArrowLeft" ? (i - 1 + options.length) % options.length
        : e.key === "Home" ? 0 : e.key === "End" ? options.length - 1 : -1;
    if (to < 0) return;
    e.preventDefault();
    onValueChange(options[to].value);
    refs.current.get(options[to].value)?.focus();
  };

  return (
    <div role="tablist" aria-label={label} onKeyDown={onKeyDown}
      className={`flex w-max max-w-full min-w-0 items-center gap-0.5 overflow-x-auto whitespace-nowrap rounded-lg border border-border bg-surface p-0.5 [scrollbar-width:none] ${className}`}>
      {options.map(o => {
        const on = o.value === value;
        return (
          <button key={o.value} type="button" role="tab" data-slot="segmented-item" id={tabId(idBase, o.value)} aria-selected={on} aria-controls={panelId(idBase)} tabIndex={on ? 0 : -1}
            ref={el => { if (el) refs.current.set(o.value, el); else refs.current.delete(o.value); }}
            onClick={() => onValueChange(o.value)}
            className={`${segmentHit} shrink-0 rounded-md px-3 py-1 text-sm outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50 ${on ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

"use client";

import {Input} from "@aoctech/ui";
import {type ComponentProps, type KeyboardEvent, useId, useState} from "react";
import {useTranslation} from "react-i18next";

import {Amount} from "@/components/ui/Amount";
import {money} from "@/lib/format";

export interface HistoryEntry {
  description: string
  amount: number
  categoryId: string
  accountId?: string
}

const MAX_SUGGESTIONS = 5;

/** What was typed before, by prefix and once per name, most recent first as given. */
function matchesFor(value: string, entries: HistoryEntry[]): HistoryEntry[] {
  const prefix = value.trim().toLocaleLowerCase();
  if (!prefix) return [];
  const seen = new Set<string>();
  return entries.filter(entry => {
    const key = entry.description.trim().toLocaleLowerCase();
    if (!key.startsWith(prefix) || seen.has(key)) return false;
    seen.add(key);
    return true;
  }).slice(0, MAX_SUGGESTIONS);
}

type InputProps = Omit<ComponentProps<typeof Input>, "value" | "onChange" | "onSelect" | "role">

/**
 * A description field that remembers: what was typed before floats under the
 * field, over what follows it. It used to be a row of chips in the form's own
 * grid, so every suggestion pushed the amount into the next cell and moved the
 * whole form; a list that takes no room moves nothing.
 *
 * A combobox: arrows move, Enter chooses (with nothing highlighted Enter still
 * submits the form), Escape closes the list and only the list. It is open only
 * while the field has focus and there is something to offer.
 */
export function SuggestingInput({value, onValueChange, entries, onSelect, onKeyDown, onFocus, onBlur, ...input}: InputProps & {
  value: string
  onValueChange: (value: string) => void
  entries: HistoryEntry[]
  onSelect: (entry: HistoryEntry) => void
}) {
  const {t} = useTranslation();
  const listId = useId();
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const [active, setActive] = useState(-1);
  const matches = matchesFor(value, entries);
  const open = focused && !dismissed && matches.length > 0;
  const optionId = (i: number) => `${listId}-${i}`;

  const choose = (entry: HistoryEntry) => {
    setDismissed(true);
    setActive(-1);
    onSelect(entry);
  };
  const keys = (e: KeyboardEvent<HTMLInputElement>) => {
    onKeyDown?.(e);
    if (!open) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive(a => (a + 1) % matches.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(a => (a <= 0 ? matches.length : a) - 1);
    } else if (e.key === "Enter" && active >= 0) {
      e.preventDefault();
      choose(matches[active]);
    } else if (e.key === "Escape") {
      // Only the list: the drawer around the form closes on Escape too.
      e.preventDefault();
      e.stopPropagation();
      setDismissed(true);
    }
  };

  return (
    <div className="relative">
      <Input
        {...input}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open && active >= 0 ? optionId(active) : undefined}
        autoComplete="off"
        value={value}
        onChange={e => { onValueChange(e.target.value); setDismissed(false); setActive(-1); }}
        onKeyDown={keys}
        onFocus={e => { setFocused(true); onFocus?.(e); }}
        onBlur={e => { setFocused(false); onBlur?.(e); }}
      />
      {open && (
        <ul
          id={listId}
          role="listbox"
          aria-label={t("bills.suggestions.title")}
          // Pressing an option must not blur the field: the choice is made on click, with the focus still in it.
          onMouseDown={e => e.preventDefault()}
          className="absolute inset-x-0 top-full z-20 mt-1 max-h-60 list-none overflow-y-auto rounded-lg border border-border bg-background p-1 shadow-card"
        >
          {matches.map((entry, i) => (
            <li
              key={`${entry.description}-${entry.categoryId}`}
              id={optionId(i)}
              role="option"
              aria-selected={i === active}
              onClick={() => choose(entry)}
              onMouseMove={() => active !== i && setActive(i)}
              className="flex min-h-9 cursor-pointer items-center justify-between gap-3 rounded-md px-2.5 py-1.5 text-sm aria-selected:bg-surface touch:min-h-11"
            >
              <span className="min-w-0 truncate">{entry.description}</span>
              <Amount tone="muted" className="shrink-0">{money(entry.amount)}</Amount>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

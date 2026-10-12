"use client"

import {money} from "@/lib/format"
import {useTranslation} from "react-i18next"

export interface HistoryEntry {
  description: string
  amount: number
  categoryId: string
  accountId?: string
}

export function HistorySuggestions({value, entries, onSelect}: {
  value: string
  entries: HistoryEntry[]
  onSelect: (entry: HistoryEntry) => void
}) {
  const {t} = useTranslation()
  const prefix = value.trim().toLocaleLowerCase()
  if (!prefix) return null
  const seen = new Set<string>()
  const matches = entries.filter(entry => {
    const name = entry.description.trim()
    const key = name.toLocaleLowerCase()
    if (!key.startsWith(prefix) || seen.has(key)) return false
    seen.add(key)
    return true
  }).slice(0, 5)
  if (!matches.length) return null
  return (
    <div role="group" aria-label={t("bills.suggestions.title")} className="-mt-2 flex flex-wrap gap-1.5">
      {matches.map(entry => (
        <button key={`${entry.description}-${entry.categoryId}`} type="button"
          onClick={() => onSelect(entry)}
          className="inline-flex min-h-8 items-center gap-2 rounded-full border border-border px-2.5 text-xs text-muted-foreground hover:bg-surface hover:text-foreground">
          <span>{entry.description}</span><span data-numeric>{money(entry.amount)}</span>
        </button>
      ))}
    </div>
  )
}

"use client"

import {CommandPalette, CommandPaletteTrigger, useCommandPaletteShortcut} from "@aoctech/ui"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {renderLink, type ShellView} from "@/components/shell/renderLink"
import {useSearchEntries} from "@/lib/search/useSearchEntries"

/** The bar's search button and the palette it opens (⌘K / Ctrl+K from anywhere). */
export function GlobalSearch({view}: {view: ShellView}) {
  const {i18n} = useTranslation()
  const [open, setOpen] = useState(false)
  const entries = useSearchEntries(view)
  useCommandPaletteShortcut(() => setOpen(o => !o))
  return (
    <>
      <CommandPaletteTrigger onClick={() => setOpen(true)} locale={i18n.language}/>
      <CommandPalette entries={entries} open={open} onOpenChange={setOpen} renderLink={renderLink} locale={i18n.language}/>
    </>
  )
}

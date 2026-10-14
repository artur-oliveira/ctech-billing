"use client";

import {CommandPalette, CommandPaletteTrigger, useCommandPaletteShortcut} from "@aoctech/ui";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {renderLink, type ShellView} from "@/components/shell/renderLink";
import {useSearchEntries} from "@/lib/search/useSearchEntries";

/**
 * The bar's search button and the palette it opens (⌘K / Ctrl+K from anywhere).
 * The palette, and with it every query that decides what is searchable, mounts
 * on the first opening: a page nobody searches from pays nothing for it.
 */
export function GlobalSearch({view}: {view: ShellView}) {
  const {i18n} = useTranslation();
  const [open, setOpen] = useState(false);
  const [seen, setSeen] = useState(false);
  const show = (next: boolean) => {
    setOpen(next);
    if (next) setSeen(true);
  };
  useCommandPaletteShortcut(() => show(!open));
  return (
    <>
      <CommandPaletteTrigger onClick={() => show(true)} locale={i18n.language}/>
      {seen && <SearchPalette view={view} open={open} onOpenChange={show} locale={i18n.language}/>}
    </>
  );
}

function SearchPalette({view, open, onOpenChange, locale}: {view: ShellView; open: boolean; onOpenChange: (open: boolean) => void; locale: string}) {
  const entries = useSearchEntries(view);
  return <CommandPalette entries={entries} open={open} onOpenChange={onOpenChange} renderLink={renderLink} locale={locale}/>;
}

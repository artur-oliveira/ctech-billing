# Global search (⌘K) and Portal at full width — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A ⌘K / Ctrl+K search over the screens and create actions of Portal, Console and Finanças (a shared `CommandPalette` in `@aoctech/ui` 0.8.0, consumed by Billing), and a Portal that uses the full page width.

**Architecture:** ctech-ui gains a dependency-free matcher (`matchEntries`), a `CommandPalette` on Base UI's Dialog, a `CommandPaletteTrigger` and a shortcut hook. Billing builds the entries from the lists that already draw the three navs, mounts the palette once in `BillingShell`, and opens create forms through the existing `requestCreate`/`useCreateRequest` wire (generalized out of `lib/finance/`), never a query parameter.

**Tech Stack:** React 19, Base UI Dialog, Tailwind 4, vitest + Testing Library (ctech-ui and Billing), `node:test` for pure logic in ctech-ui, Next.js 16 static export, react-i18next JSON catalogs.

**Spec:** `docs/specs/2026-10-14-global-search-and-portal-width.md`

Repos: `P_UI=/home/artur-revgas/Documents/Projects/Ctech/ctech-ui`, `P_BILL=/home/artur-revgas/Documents/Projects/Ctech/ctech-billing` (frontend in `P_BILL/ui`). Part A runs in `P_UI`, Part B in `P_BILL`. **Part B starts only after 0.8.0 is on npm.**

## Global Constraints

- No Fuse.js or any new dependency in `@aoctech/ui` (spec §3.2, §7).
- Create requests are **never** a URL parameter; `requestCreate`'s ≤3 s hold, take-once and drop-by-target semantics are unchanged (spec §4.2).
- Routes in English, Portuguese only in i18n text (project memory). Copy goes in `locales/{pt-BR,en}/common.json`.
- The palette's rows are ≥44px under touch and its input font ≥16px on a phone; no colours of its own (tokens only).
- No attribution trailers in commits or PR bodies (user's global rule), and plans/specs live under `docs/specs|plans/`.
- Entries come from the nav lists (`NAV`, `consoleNav`, `visibleFinanceSections`); no second registry (spec §4.1).
- No infrastructure, API or cost change in either repo.

## Review Focus

- A diacritic-less query ("cartao", "recorrencia") must find "Cartões"/"Recorrências"; an accented query must too. Pinned in Task 1.
- A query that matches nothing shows the empty state and Enter does nothing (no crash on `results[cursor]` undefined). Pinned in Task 3.
- Opening the palette twice starts clean (query and cursor reset). Pinned in Task 3.
- A person with no organization sees no Console entries; one with no billing account sees no Portal entries; a viewer role sees no create actions. Pinned in Task 7.
- A create request for a screen that is not yet mounted survives the navigation and opens the drawer once; a request nobody takes is dropped. Pinned in Tasks 5 and 6.
- Selecting an entry for the screen already open must still work (action opens in place; page link is a no-op navigation). Pinned in Tasks 6 and 8.

---

## Part A — ctech-ui 0.8.0 (`P_UI`)

Branch: `feat/command-palette` (from an up-to-date `main`; it is at 0.7.0, `git pull --ff-only` was done when this plan was written).

### Task 1: The matcher

**Files:**
- Create: `src/lib/match.ts`
- Create: `src/lib/match.test.ts` (node:test, like `src/lib/i18n.test.ts`)
- Modify: `package.json` (the `test` script)

**Interfaces:**
- Produces:
```ts
type Matchable = {label: string; group?: string; keywords?: readonly string[]}
function foldText(s: string): string
function matchEntries<T extends Matchable>(entries: readonly T[], query: string, limit?: number): T[]  // limit default 8
```

- [ ] **Step 1: Write the failing test** — `src/lib/match.test.ts`

```ts
import assert from "node:assert/strict"
import {test} from "node:test"

import {foldText, matchEntries} from "./match.ts"

const E = [
  {id: "1", label: "Agenda", group: "Finanças", keywords: ["contas a pagar", "boletos"]},
  {id: "2", label: "Recorrências", group: "Finanças"},
  {id: "3", label: "Nova recorrência", group: "Finanças"},
  {id: "4", label: "Cartões", group: "Finanças", keywords: ["fatura"]},
  {id: "5", label: "Faturas", group: "Portal"},
  {id: "6", label: "Faturas", group: "Console"},
]
const ids = (q: string, limit?: number) => matchEntries(E, q, limit).map(e => e.id)

test("folds case, accents and spaces", () => {
  assert.equal(foldText("  Recorrências  Ç "), "recorrencias c")
})

test("an empty query keeps the given order, capped at the limit", () => {
  assert.deepEqual(ids(""), ["1", "2", "3", "4", "5", "6"])
  assert.deepEqual(ids("   ", 2), ["1", "2"])
})

test("accent-insensitive in both directions", () => {
  assert.deepEqual(ids("cartao"), ["4"])
  assert.deepEqual(ids("cartões"), ["4"])
  assert.deepEqual(ids("recorrencia"), ["2", "3"])
})

test("a label that starts with the query beats a label word that does", () => {
  assert.deepEqual(ids("recor"), ["2", "3"])
})

test("a label hit outranks a keyword hit, a keyword outranks the group", () => {
  assert.deepEqual(ids("fatura"), ["5", "6", "4"]) // labels first (given order), then Cartões by keyword
  assert.deepEqual(ids("financas").length, 4) // group-only match still finds the group's entries
})

test("every token must match, in any order", () => {
  assert.deepEqual(ids("nova rec"), ["3"])
  assert.deepEqual(ids("rec nova"), ["3"])
  assert.deepEqual(ids("nova xyz"), [])
})

test("ties keep the given order and the limit applies after ranking", () => {
  assert.deepEqual(ids("faturas", 1), ["5"])
})

test("no match is an empty list", () => {
  assert.deepEqual(ids("zzz"), [])
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd $P_UI && node --test src/lib/match.test.ts`
Expected: FAIL — `Cannot find module './match.ts'`.

- [ ] **Step 3: Implement** — `src/lib/match.ts`

```ts
/**
 * The palette's matcher: pure, dependency-free, accent-insensitive. The
 * label always outranks a keyword, and a keyword outranks the group, so what a
 * screen is called wins over what people also call it.
 */

type Matchable = {label: string; group?: string; keywords?: readonly string[]}

/** Lowercase, no diacritics, single spaces. */
function foldText(s: string): string {
  return s.normalize("NFD").replace(/\p{M}/gu, "").toLowerCase().replace(/\s+/g, " ").trim()
}

const startsWord = (text: string, token: string) => text.split(" ").some(w => w.startsWith(token))

/** The best score one token earns in an entry; 0 means the token is nowhere in it. */
function tokenScore(label: string, keywords: string[], group: string, token: string): number {
  if (label.startsWith(token)) return 100
  if (startsWord(label, token)) return 80
  if (label.includes(token)) return 60
  if (keywords.some(k => startsWord(k, token))) return 40
  if (keywords.some(k => k.includes(token))) return 30
  if (group.includes(token)) return 10
  return 0
}

function matchEntries<T extends Matchable>(entries: readonly T[], query: string, limit = 8): T[] {
  const folded = foldText(query)
  if (!folded) return entries.slice(0, limit)
  const tokens = folded.split(" ")
  return entries
    .map((entry, index) => {
      const label = foldText(entry.label)
      const keywords = (entry.keywords ?? []).map(foldText)
      const group = foldText(entry.group ?? "")
      let score = label.startsWith(folded) ? 50 : 0
      for (const token of tokens) {
        const s = tokenScore(label, keywords, group, token)
        if (s === 0) return {entry, index, score: 0}
        score += s
      }
      return {entry, index, score}
    })
    .filter(r => r.score > 0)
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .slice(0, limit)
    .map(r => r.entry)
}

export {foldText, matchEntries, type Matchable}
```

- [ ] **Step 4: Wire the test into `npm test`** — in `package.json` change
`"test": "node --test src/lib/i18n.test.ts && vitest run ..."` to
`"test": "node --test src/lib/i18n.test.ts src/lib/match.test.ts && vitest run --maxWorkers=2 && npm run build"`.

- [ ] **Step 5: Run to verify it passes**

Run: `cd $P_UI && node --test src/lib/match.test.ts`
Expected: all PASS. (If `ids("fatura")` ordering fails, the scores are the contract: label-start 100 > word-start 80 > label-contains 60 > keyword 40/30 > group 10; fix the code, not the test.)

- [ ] **Step 6: Commit**

```bash
git checkout -b feat/command-palette
git add src/lib/match.ts src/lib/match.test.ts package.json
git commit -m "feat(command-palette): accent-insensitive matcher"
```

### Task 2: Labels

**Files:**
- Modify: `src/lib/i18n.ts`
- Modify: `src/lib/i18n.test.ts`

**Interfaces:**
- Produces: `getCommandPaletteLabels(locale, overrides?)`, type `CommandPaletteLabels = {dialog; placeholder; empty; action; trigger; results; close}` all `string`.

- [ ] **Step 1: Failing test** — append to `src/lib/i18n.test.ts`

```ts
test("command palette copy per locale, with overrides", () => {
  assert.equal(getCommandPaletteLabels("pt-BR").trigger, "Buscar")
  assert.equal(getCommandPaletteLabels("en").empty, "Nothing found for")
  assert.equal(getCommandPaletteLabels("en", {trigger: "Find"}).trigger, "Find")
  assert.equal(getCommandPaletteLabels("en", {trigger: "Find"}).action, "Action")
})
```
and add `getCommandPaletteLabels` to the import line at the top of that file.

- [ ] **Step 2: Run** `node --test src/lib/i18n.test.ts` — Expected: FAIL (not exported).

- [ ] **Step 3: Implement** — in `src/lib/i18n.ts`, after `selectMessages`:

```ts
const commandPaletteMessages = {
  "pt-BR": {
    dialog: "Buscar no aplicativo",
    placeholder: "Buscar telas e ações…",
    empty: "Nada encontrado para",
    action: "Ação",
    trigger: "Buscar",
    results: "Resultados",
    close: "Fechar",
  },
  en: {
    dialog: "Search the app",
    placeholder: "Search screens and actions…",
    empty: "Nothing found for",
    action: "Action",
    trigger: "Search",
    results: "Results",
    close: "Close",
  },
} as const
```
Add `type CommandPaletteLabels = {-readonly [K in keyof (typeof commandPaletteMessages)["en"]]: string}`, the getter

```ts
function getCommandPaletteLabels(locale: Locale | undefined, overrides?: Partial<CommandPaletteLabels>): CommandPaletteLabels {
  return {...commandPaletteMessages[resolveCatalog(locale)], ...overrides}
}
```
and add both to the `export {…}` block.

- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -am "feat(command-palette): pt-BR and en labels"`

### Task 3: `CommandPalette`

**Files:**
- Create: `src/components/command-palette.tsx`
- Create: `src/components/command-palette.test.tsx`
- Modify: `src/index.ts`

**Interfaces:**
- Consumes: `matchEntries`, `getCommandPaletteLabels`, `defaultRenderLink`/`RenderLink`, `useThemeScope` (as `modal.tsx`).
- Produces:
```ts
interface CommandEntry {
  id: string; label: string; group: string; kind: "page" | "action"
  keywords?: string[]; icon?: ReactNode
  href?: string            // page: rendered through renderLink
  onSelect?: () => void    // action: called right after the dialog closes
}
interface CommandPaletteProps {
  entries: CommandEntry[]; open: boolean; onOpenChange: (open: boolean) => void
  renderLink?: RenderLink; locale?: Locale; labels?: Partial<CommandPaletteLabels>
  limit?: number; density?: Density
}
function CommandPalette(props: CommandPaletteProps): JSX.Element
```

- [ ] **Step 1: Failing tests** — `src/components/command-palette.test.tsx`

```tsx
import {render, screen, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {useState} from "react"
import {describe, expect, it, vi} from "vitest"

import {CommandPalette, type CommandEntry} from "./command-palette"

const onNew = vi.fn()
const ENTRIES: CommandEntry[] = [
  {id: "bills", label: "Agenda", group: "Finanças", kind: "page", href: "/finance/bills", keywords: ["boletos"]},
  {id: "rec", label: "Recorrências", group: "Finanças", kind: "page", href: "/finance/recurrences"},
  {id: "rec-new", label: "Nova recorrência", group: "Finanças", kind: "action", onSelect: onNew},
  {id: "inv", label: "Faturas", group: "Portal", kind: "page", href: "/invoices"},
]

function Harness({entries = ENTRIES}: {entries?: CommandEntry[]}) {
  const [open, setOpen] = useState(true)
  return (
    <>
      <button onClick={() => setOpen(true)}>abrir</button>
      <CommandPalette entries={entries} open={open} onOpenChange={setOpen} />
    </>
  )
}

const options = () => within(screen.getByRole("listbox")).getAllByRole("option").map(o => o.textContent)

describe("CommandPalette", () => {
  it("opens with the input focused, grouped by subproduct, as a combobox over a listbox", async () => {
    render(<Harness />)
    const input = await screen.findByRole("combobox", {name: "Buscar no aplicativo"})
    expect(input).toHaveFocus()
    expect(input).toHaveAttribute("aria-expanded", "true")
    expect(screen.getByText("Portal")).toBeInTheDocument() // group heading with an empty query
    expect(options()).toHaveLength(4)
  })

  it("filters as you type and ignores accents", async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.type(await screen.findByRole("combobox"), "recorrencia")
    expect(options().map(t => t?.includes("Nova recorrência") || t?.includes("Recorrências"))).toEqual([true, true])
  })

  it("moves with the arrows, wrapping, and selects with Enter", async () => {
    const user = userEvent.setup()
    render(<Harness />)
    const input = await screen.findByRole("combobox")
    await user.type(input, "rec")
    expect(screen.getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true")
    await user.keyboard("{ArrowDown}")
    expect(screen.getAllByRole("option")[1]).toHaveAttribute("aria-selected", "true")
    expect(input).toHaveAttribute("aria-activedescendant", screen.getAllByRole("option")[1].id)
    await user.keyboard("{ArrowDown}")
    expect(screen.getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true")
    await user.keyboard("{ArrowUp}")
    expect(screen.getAllByRole("option")[1]).toHaveAttribute("aria-selected", "true")
  })

  it("a page is a real link and closes the dialog", async () => {
    const user = userEvent.setup()
    render(<Harness />)
    const link = await screen.findByRole("option", {name: /Faturas/})
    expect(link).toHaveAttribute("href", "/invoices")
    await user.click(link)
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  })

  it("an action closes the dialog and then runs onSelect, from Enter too", async () => {
    const user = userEvent.setup()
    onNew.mockClear()
    render(<Harness />)
    await user.type(await screen.findByRole("combobox"), "nova rec")
    expect(screen.getByRole("option", {name: /Ação/})).toBeInTheDocument()
    await user.keyboard("{Enter}")
    expect(onNew).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  })

  it("shows the empty state for no match and Enter does nothing", async () => {
    const user = userEvent.setup()
    onNew.mockClear()
    render(<Harness />)
    await user.type(await screen.findByRole("combobox"), "zzzz{Enter}")
    expect(screen.getByText(/Nada encontrado para/)).toBeInTheDocument()
    expect(screen.queryAllByRole("option")).toHaveLength(0)
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("Escape closes it, and reopening starts clean", async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.type(await screen.findByRole("combobox"), "agenda")
    await user.keyboard("{Escape}")
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    await user.click(screen.getByText("abrir"))
    expect(await screen.findByRole("combobox")).toHaveValue("")
    expect(options()).toHaveLength(4)
  })

  it("speaks English for an English locale", async () => {
    render(<CommandPalette entries={ENTRIES} open onOpenChange={() => {}} locale="en" />)
    expect(await screen.findByRole("combobox", {name: "Search the app"})).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run** `cd $P_UI && npx vitest run src/components/command-palette.test.tsx` — Expected: FAIL (module not found).

- [ ] **Step 3: Implement** — `src/components/command-palette.tsx`

```tsx
"use client"

import {Dialog} from "@base-ui/react/dialog"
import {useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode} from "react"

import {cn} from "../lib/cn"
import {DEFAULT_LOCALE, getCommandPaletteLabels, type CommandPaletteLabels, type Locale} from "../lib/i18n"
import {defaultRenderLink, type RenderLink} from "../lib/link"
import {matchEntries} from "../lib/match"
import {useThemeScope, type Density} from "./theme-provider"

interface CommandEntry {
  id: string
  label: string
  /** The subproduct (or section) the entry belongs to; shown as context. */
  group: string
  /** An action does something (opens a form); a page goes somewhere. */
  kind: "page" | "action"
  keywords?: string[]
  icon?: ReactNode
  /** A page: rendered through `renderLink`, so it is a real link. */
  href?: string
  /** An action: called right after the dialog closes. */
  onSelect?: () => void
}

interface CommandPaletteProps {
  entries: CommandEntry[]
  open: boolean
  onOpenChange: (open: boolean) => void
  renderLink?: RenderLink
  locale?: Locale
  labels?: Partial<CommandPaletteLabels>
  /** Results shown. Defaults to 8. */
  limit?: number
  /** The palette renders in a portal; it follows the nearest DensityScope unless told otherwise. */
  density?: Density
}

const ROW = cn(
  "flex w-full min-h-11 items-center gap-3 rounded-md px-2.5 py-2 text-left outline-none",
  "in-data-[density=compact]:min-h-9 touch:min-h-11",
  "aria-selected:bg-surface [&_svg]:size-4 [&_svg]:shrink-0"
)

/**
 * ⌘K navigation: a search over the screens and actions an app hands it. The
 * app owns the entries and `open`; the palette owns matching, keyboard and
 * focus. Built on Base UI's Dialog (focus trap, Escape, scroll lock). The body
 * mounts only while open, so each opening starts with an empty query.
 */
function CommandPalette({entries, open, onOpenChange, renderLink = defaultRenderLink, locale = DEFAULT_LOCALE, labels, limit = 8, density: densityProp}: CommandPaletteProps) {
  const text = getCommandPaletteLabels(locale, labels)
  const scope = useThemeScope()
  const density = densityProp ?? scope.density
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal data-ctech-theme={scope.theme} data-density={density}>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[1px] transition-opacity duration-200 data-[ending-style]:opacity-0 data-[starting-style]:opacity-0 motion-reduce:transition-none" />
        <Dialog.Popup
          aria-label={text.dialog}
          className="fixed top-[10vh] left-1/2 z-50 w-[calc(100vw-2rem)] max-w-lg -translate-x-1/2 overflow-hidden rounded-xl border border-border bg-background text-foreground shadow-modal outline-none"
        >
          <Dialog.Title className="sr-only">{text.dialog}</Dialog.Title>
          <Body entries={entries} limit={limit} text={text} renderLink={renderLink} close={() => onOpenChange(false)} />
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

function Body({entries, limit, text, renderLink, close}: {entries: CommandEntry[]; limit: number; text: CommandPaletteLabels; renderLink: RenderLink; close: () => void}) {
  const [query, setQuery] = useState("")
  const [cursor, setCursor] = useState(0)
  const listId = useId()
  const refs = useRef<(HTMLElement | null)[]>([])
  const results = useMemo(() => matchEntries(entries, query, limit), [entries, query, limit])
  const at = results.length === 0 ? -1 : Math.min(cursor, results.length - 1)

  useEffect(() => {
    refs.current[at]?.scrollIntoView?.({block: "nearest"})
  }, [at])

  const onKeyDown = (e: KeyboardEvent) => {
    if (results.length === 0) return
    if (e.key === "ArrowDown") {
      e.preventDefault()
      setCursor((at + 1) % results.length)
    } else if (e.key === "ArrowUp") {
      e.preventDefault()
      setCursor((at - 1 + results.length) % results.length)
    } else if (e.key === "Enter") {
      e.preventDefault()
      refs.current[at]?.click()
    }
  }

  const grouped = query.trim() === ""
  return (
    <div>
      <div className="border-b border-border px-3">
        <input
          autoFocus
          role="combobox"
          aria-label={text.dialog}
          aria-expanded
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={at >= 0 ? `${listId}-${at}` : undefined}
          autoComplete="off"
          spellCheck={false}
          value={query}
          placeholder={text.placeholder}
          onChange={e => {
            setQuery(e.target.value)
            setCursor(0)
          }}
          onKeyDown={onKeyDown}
          className="h-12 w-full bg-transparent text-base text-foreground outline-none placeholder:text-muted-foreground md:text-sm"
        />
      </div>
      <ul id={listId} role="listbox" aria-label={text.results} className="m-0 max-h-80 list-none overflow-y-auto p-1.5">
        {results.length === 0 && (
          <li role="presentation" className="px-3 py-6 text-center text-sm text-muted-foreground">
            {text.empty} <b className="text-foreground">{query}</b>
          </li>
        )}
        {results.map((entry, i) => {
          const heading = grouped && (i === 0 || results[i - 1].group !== entry.group)
          const body = (
            <>
              {entry.icon && <span className="text-muted-foreground" aria-hidden>{entry.icon}</span>}
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">{entry.label}</span>
                {!grouped && <span className="block truncate text-xs text-muted-foreground">{entry.group}</span>}
              </span>
              {entry.kind === "action" && <span className="shrink-0 text-xs text-muted-foreground">{text.action}</span>}
            </>
          )
          const common = {
            id: `${listId}-${i}`,
            role: "option",
            "aria-selected": i === at,
            className: ROW,
            onMouseEnter: () => setCursor(i),
          } as const
          return (
            <li key={entry.id} role="presentation">
              {heading && <p aria-hidden className="m-0 px-2.5 pt-2 pb-1 text-xs text-muted-foreground">{entry.group}</p>}
              {entry.kind === "page" && entry.href ? (
                renderLink({...common, href: entry.href, onClick: close, ref: el => { refs.current[i] = el }, children: body})
              ) : (
                <button
                  {...common}
                  type="button"
                  ref={el => { refs.current[i] = el }}
                  onClick={() => {
                    close()
                    entry.onSelect?.()
                  }}
                >
                  {body}
                </button>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

export {CommandPalette, type CommandEntry, type CommandPaletteProps}
```

- [ ] **Step 4: Export** — in `src/index.ts` add
`export {CommandPalette, type CommandEntry, type CommandPaletteProps} from "./components/command-palette"` and `type CommandPaletteLabels` to the i18n type export line.

- [ ] **Step 5: Run** `npx vitest run src/components/command-palette.test.tsx` — Expected: PASS. Typical fixes: if `renderLink` rejects `ref`/`children` typing, they are already on `LinkProps` (`AnchorHTMLAttributes` + `ref`); if the `option` accessible name test for the action fails, the "Ação" text is inside the button so `name: /Ação/` matches.

- [ ] **Step 6: Commit** — `git add -A && git commit -m "feat(command-palette): CommandPalette on Base UI Dialog"`

### Task 4: Trigger and shortcut

**Files:**
- Modify: `src/components/command-palette.tsx` (append)
- Modify: `src/components/command-palette.test.tsx` (append)
- Modify: `src/index.ts`

**Interfaces:**
- Produces:
```ts
function CommandPaletteTrigger(props: {onClick: () => void; locale?: Locale; labels?: Partial<CommandPaletteLabels>; className?: string}): JSX.Element
function useCommandPaletteShortcut(onToggle: () => void): void   // ⌘K / Ctrl+K on window
```

- [ ] **Step 1: Failing tests** — append

```tsx
import {fireEvent} from "@testing-library/react"
import {CommandPaletteTrigger, useCommandPaletteShortcut} from "./command-palette"

function Shortcut({onToggle}: {onToggle: () => void}) {
  useCommandPaletteShortcut(onToggle)
  return <input aria-label="campo" />
}

describe("CommandPaletteTrigger", () => {
  it("is a named button with the shortcut hint", async () => {
    const onClick = vi.fn()
    render(<CommandPaletteTrigger onClick={onClick} />)
    const button = screen.getByRole("button", {name: "Buscar"})
    await userEvent.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(button).toHaveAttribute("aria-keyshortcuts", "Control+K Meta+K")
  })
})

describe("useCommandPaletteShortcut", () => {
  it("toggles on Ctrl+K and Cmd+K, even from a field, and prevents the browser's own", () => {
    const onToggle = vi.fn()
    render(<Shortcut onToggle={onToggle} />)
    screen.getByLabelText("campo").focus()
    expect(fireEvent.keyDown(window, {key: "k", ctrlKey: true})).toBe(false) // preventDefault called
    fireEvent.keyDown(window, {key: "K", metaKey: true})
    expect(onToggle).toHaveBeenCalledTimes(2)
  })

  it("ignores a plain k, Shift+Ctrl+K and a removed listener", () => {
    const onToggle = vi.fn()
    const {unmount} = render(<Shortcut onToggle={onToggle} />)
    fireEvent.keyDown(window, {key: "k"})
    fireEvent.keyDown(window, {key: "k", ctrlKey: true, shiftKey: true})
    unmount()
    fireEvent.keyDown(window, {key: "k", ctrlKey: true})
    expect(onToggle).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run** — Expected: FAIL (not exported).

- [ ] **Step 3: Implement** — append to `command-palette.tsx` (add `useEffect` already imported; import `Button`-free: a plain button styled with tokens):

```tsx
function CommandPaletteTrigger({onClick, locale = DEFAULT_LOCALE, labels, className}: {onClick: () => void; locale?: Locale; labels?: Partial<CommandPaletteLabels>; className?: string}) {
  const text = getCommandPaletteLabels(locale, labels)
  const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={text.trigger}
      aria-keyshortcuts="Control+K Meta+K"
      className={cn(
        "inline-flex h-8 items-center gap-2 rounded-lg border border-border bg-background px-2.5 text-sm text-muted-foreground outline-none",
        "hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 touch:min-h-11 touch:min-w-11 touch:justify-center",
        "max-sm:w-8 max-sm:justify-center max-sm:px-0 [&_svg]:size-4 [&_svg]:shrink-0",
        className
      )}
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
        <circle cx="11" cy="11" r="7" />
        <path d="m20 20-3.5-3.5" />
      </svg>
      <span className="max-sm:hidden">{text.trigger}</span>
      <kbd className="rounded border border-border px-1.5 text-xs font-medium max-sm:hidden touch:hidden">{mac ? "⌘K" : "Ctrl K"}</kbd>
    </button>
  )
}

/** ⌘K / Ctrl+K anywhere, including from a field: the key is not typed text. */
function useCommandPaletteShortcut(onToggle: () => void): void {
  const ref = useRef(onToggle)
  useEffect(() => {
    ref.current = onToggle
  })
  useEffect(() => {
    const onKeyDown = (e: globalThis.KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault()
        ref.current()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [])
}
```
Update the file's export line to include `CommandPaletteTrigger, useCommandPaletteShortcut`; in `index.ts` export them too.

- [ ] **Step 4: Run** `npx vitest run src/components/command-palette.test.tsx` — Expected: PASS.
- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(command-palette): trigger button and Ctrl/Cmd+K hook"`

### Task 5: Story, docs, version, PR

**Files:**
- Modify: `src/navigation.stories.tsx` (a `CommandPalette` story), `docs/component-inventory.md`, `README.md` (a short "CommandPalette" section under Navigation), `package.json` + `package-lock.json` (version 0.8.0)

- [ ] **Step 1: Story** — append to `src/navigation.stories.tsx` a story that renders `CommandPaletteTrigger` + `CommandPalette` with ~6 mixed page/action entries over two groups ("Portal", "Finanças"), controlled by `useState`, following the file's existing meta/StoryObj shape.
- [ ] **Step 2: Docs** — in `docs/component-inventory.md` add `CommandPalette` (+ `CommandPaletteTrigger`, `matchEntries`) to the "Navegação" row. In `README.md` document the entry shape, that the app owns `entries`/`open`, that `href` goes through `renderLink`, and the shortcut hook, in the style of the surrounding sections.
- [ ] **Step 3: Version** — `npm version 0.8.0 --no-git-tag-version`.
- [ ] **Step 4: Full verification** — Run: `cd $P_UI && npm test` — Expected: node tests, all vitest suites and `tsc` build PASS. Fix any type error from `tsc` (the build excludes tests).
- [ ] **Step 5: Commit, push, PR**

```bash
git add -A && git commit -m "feat: CommandPalette (v0.8.0)"
git push -u origin feat/command-palette
gh pr create -R artur-oliveira/ctech-ui --title "feat: CommandPalette (v0.8.0)" --body "<what/why/tests; no attribution lines>"
```
- [ ] **Step 6: Merge when CI is green** (merge autonomy covers implementation PRs).
- [ ] **Step 7: Release — ASK FIRST.** Publishing a GitHub Release triggers `npm publish` of a public package; it is outward-facing. Confirm with the user, then `gh release create v0.8.0 -R artur-oliveira/ctech-ui --title v0.8.0 --notes "<notes>"` and wait for the Publish workflow (`gh run watch`). Verify: `npm view @aoctech/ui@0.8.0 version`.

---

## Part B — ctech-billing (`P_BILL/ui`)

Branch: `feat/global-search` from `main` **after** the spec+plan PR (`docs/spec-global-search`) is merged. Run tests with `cd P_BILL/ui && npx vitest run <file>`; full gate `npm run lint && npx tsc --noEmit && npm test`.

### Task 6: `createRequest` becomes the app's, with `price` and `webhook`

**Files:**
- Move: `src/lib/finance/createRequest.ts` → `src/lib/createRequest.ts` (`git mv`)
- Modify imports in: `StatementView.tsx`, `RecurrencesView.tsx`, `BillsView.tsx`, `CardsView.tsx`, `AccountsView.tsx`, `FinanceBottomNav.tsx` and the five `*.test.tsx` that `import * as createRequest|create from "@/lib/finance/createRequest"` (`grep -rln "lib/finance/createRequest" src`)
- Modify: `src/lib/createRequest.ts` (`CreateKind`), `FinanceBottomNav.tsx` (`ACTION_ICON` is `Record<CreateKind, …>`, must cover the new kinds)
- Modify: `src/app/(console)/console/catalog/page.tsx`, `src/app/(console)/console/webhooks/page.tsx`
- Create: `src/lib/createRequest.test.ts`
- Test: extend the two console page tests if present (`ls src/app/\(console\)/console/catalog src/app/\(console\)/console/webhooks`), otherwise create `catalog.createRequest.test.tsx`-style tests following the neighbouring `periods.test.tsx` setup.

**Interfaces:**
- Produces: `import {requestCreate, takePendingCreate, dropCreateUnlessAt, clearPendingCreate, useCreateRequest, type CreateKind} from "@/lib/createRequest"` with `CreateKind = "bill" | "purchase" | "recurrence" | "transfer" | "account" | "price" | "webhook"`.

- [ ] **Step 1: Failing test** — `src/lib/createRequest.test.ts`

```ts
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest"

import {clearPendingCreate, dropCreateUnlessAt, requestCreate, takePendingCreate} from "@/lib/createRequest"

beforeEach(() => vi.useFakeTimers())
afterEach(() => {
  clearPendingCreate()
  vi.useRealTimers()
})

describe("createRequest", () => {
  it("is taken once, by its own kind only", () => {
    requestCreate("price")
    expect(takePendingCreate("webhook")).toBe(false)
    expect(takePendingCreate("price")).toBe(true)
    expect(takePendingCreate("price")).toBe(false)
  })

  it("is dropped after three seconds", () => {
    requestCreate("webhook", "/console/webhooks")
    vi.advanceTimersByTime(3_001)
    expect(takePendingCreate("webhook")).toBe(false)
  })

  it("is dropped when the person lands anywhere but its target", () => {
    requestCreate("price", "/console/catalog")
    dropCreateUnlessAt("/console/overview")
    expect(takePendingCreate("price")).toBe(false)
    requestCreate("price", "/console/catalog")
    dropCreateUnlessAt("/console/catalog")
    expect(takePendingCreate("price")).toBe(true)
  })
})
```

- [ ] **Step 2: Run** `npx vitest run src/lib/createRequest.test.ts` — Expected: FAIL (module not found).

- [ ] **Step 3: Move and widen**

```bash
git mv src/lib/finance/createRequest.ts src/lib/createRequest.ts
grep -rl '@/lib/finance/createRequest' src | xargs sed -i 's#@/lib/finance/createRequest#@/lib/createRequest#g'
```
In `src/lib/createRequest.ts` set `export type CreateKind = "bill" | "purchase" | "recurrence" | "transfer" | "account" | "price" | "webhook"` and edit the doc comment's first paragraph: "The phone's central create button and the global search both live in the shell; the drawer lives in the screen." In `FinanceBottomNav.tsx` extend `ACTION_ICON` with `price: <Plus/>, webhook: <Plus/>`.

- [ ] **Step 4: Screens take the request** — `catalog/page.tsx`: import `useCreateRequest` from `@/lib/createRequest` and, after the `creating` state, add `useCreateRequest("price", () => setCreating(true))`. `webhooks/page.tsx`: after the `creating` state, add `useCreateRequest("webhook", () => { if (!addsRefused) setCreating(true) })` — the button is also hidden for a suspended tenant, so the request must not bypass it. (The `atLimit` case: the screen's modal stays closed via the existing `disabled`; keep it simple — also guard `!atLimit` where `atLimit` is computed; since `atLimit` is derived below the state, define the callback with the values read at call time: `useCreateRequest("webhook", () => { if (!addsRefused && !(query.data && query.data.data.length >= (query.data.max_endpoints ?? limits.maxWebhookEndpoints))) setCreating(true) })`.)
  Tests (add to the existing page tests, or create `catalog.test.tsx` / `webhooks.test.tsx` beside the page following `periods.test.tsx`'s providers/mocks): render the page with data mocked, call `requestCreate("price")` before/after mount inside `act`, assert the create dialog (`getByRole("dialog")`) opens; for webhooks assert it does **not** open when the list is at `max_endpoints` and does when under it.

- [ ] **Step 5: Run the touched suites** — `npx vitest run src/lib/createRequest.test.ts src/components/finance src/app/\(console\)` — Expected: PASS; `npx tsc --noEmit` clean.
- [ ] **Step 6: Commit** — `git add -A && git commit -m "refactor(create): the create request is the app's, not Finanças'; price and webhook open on request"`

### Task 7: Portal at full width and the shared Portal nav

**Files:**
- Create: `src/lib/portal/nav.ts`
- Modify: `src/app/(portal)/layout.tsx`, `src/components/portal/PortalBottomNav.tsx` (optional: read `PORTAL_NAV`)
- Modify (after the visual review, only where needed): Portal screens/components under `src/app/(portal)/**` and `src/components/portal/**`

**Interfaces:**
- Produces: `export const PORTAL_NAV = [{href: "/dashboard", labelKey: "portal.nav.home"}, {href: "/invoices", labelKey: "portal.nav.invoices"}, {href: "/subscriptions", labelKey: "portal.nav.subscriptions"}] as const`

- [ ] **Step 1:** Create `src/lib/portal/nav.ts` with `PORTAL_NAV` above; in `(portal)/layout.tsx` delete the local `NAV`, `import {PORTAL_NAV} from "@/lib/portal/nav"`, and use it in the `SidebarNav` groups.
- [ ] **Step 2: Width** — delete `contentClassName="max-w-2xl"` and the comment above it in `(portal)/layout.tsx`; update the doc comment ("The portal reads one bill at a time…" no longer applies; say the pages choose their own measure).
- [ ] **Step 3: Existing tests** — `npx vitest run src/components/portal "src/app/(portal)"` — Expected: PASS (nothing asserted the 2xl class; if something does, update it to the new truth).
- [ ] **Step 4: Visual review** — `cd P_BILL/ui && npm run dev`, then with chrome-devtools at 1440×900 and 390×844 visit `/dashboard`, `/invoices`, `/invoice?…`, `/subscriptions`, `/subscription?…`, the terms gate and the no-billing-account state (the dev mock data exists under `src/dev/`; check `README`/`AGENTS.md` for how to sign in against it). For each screen take a screenshot; fix what stretches badly: paragraph-only blocks get `max-w-prose`, forms get `max-w-xl`, lists/cards use `grid lg:grid-cols-2` (cards) or stay full-width tables. Re-run the portal suites after each fix.
- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(ui): portal uses the page width; nav list shared"`

### Task 8: Bump the dependency

- [ ] **Step 1:** `cd P_BILL/ui && npm install @aoctech/ui@^0.8.0` (confirm `npm view @aoctech/ui version` is 0.8.0 first). Expected: `package.json` `^0.8.0`, lockfile updated, no other package changes (`git diff --stat package-lock.json`).
- [ ] **Step 2:** `npx tsc --noEmit && npx vitest run` — Expected: PASS (0.7→0.8 is additive).
- [ ] **Step 3: Commit** — `git add package.json package-lock.json && git commit -m "chore(ui): @aoctech/ui 0.8.0"`

### Task 9: The entries

**Files:**
- Create: `src/lib/search/entries.ts`
- Create: `src/lib/search/entries.test.ts`

**Interfaces:**
- Consumes: `PORTAL_NAV`, `consoleNav` (`@/lib/console/nav`), `FINANCE_SECTIONS`/`visibleFinanceSections` (`@/components/finance/FinanceNav`), `CreateKind`, `requestCreate`.
- Produces:
```ts
type SearchContext = {
  portal: boolean                       // has a billing account
  console: {reviewer: boolean; active: boolean} | null   // null: no organization
  finance: {personal: boolean; canConfigure: boolean; canWrite: boolean}
}
type SearchText = (key: string) => string       // i18next t, or undefined-safe keyword lookup
type SearchEntry = import("@aoctech/ui").CommandEntry
function buildSearchEntries(ctx: SearchContext, t: SearchText, go: (href: string, kind?: CreateKind) => void, current: "portal"|"console"|"finance"): SearchEntry[]
```
The returned order puts the **current** subproduct first, then the other two, each in its nav order, so the empty query lists what the person is most likely to want.

- [ ] **Step 1: Failing tests** — `src/lib/search/entries.test.ts`

```ts
import {describe, expect, it, vi} from "vitest"

import {buildSearchEntries, type SearchContext} from "@/lib/search/entries"

const t = (key: string) => key // keys are enough to assert on
const go = vi.fn()
const base: SearchContext = {
  portal: true,
  console: {reviewer: false, active: true},
  finance: {personal: true, canConfigure: true, canWrite: true},
}
const ids = (ctx: Partial<SearchContext>, current: "portal" | "console" | "finance" = "finance") =>
  buildSearchEntries({...base, ...ctx}, t, go, current).map(e => e.id)

describe("buildSearchEntries", () => {
  it("lists the current subproduct first, each in its nav order", () => {
    const list = ids({}, "console")
    expect(list[0]).toBe("console:/console/overview")
    expect(list.findIndex(i => i.startsWith("portal:"))).toBeGreaterThan(list.findIndex(i => i.startsWith("console:")))
  })

  it("has no Console entries without an organization, and no Portal ones without a billing account", () => {
    expect(ids({console: null}).some(i => i.startsWith("console:"))).toBe(false)
    expect(ids({portal: false}).some(i => i.startsWith("portal:"))).toBe(false)
  })

  it("Console without a tenant in this mode offers nothing from the console", () => {
    expect(ids({console: {reviewer: false, active: false}}).some(i => i.startsWith("console:"))).toBe(false)
  })

  it("Solicitações only for a reviewer", () => {
    expect(ids({}).includes("console:/console/applications")).toBe(false)
    expect(ids({console: {reviewer: true, active: true}}).includes("console:/console/applications")).toBe(true)
  })

  it("Plano only in a personal space, Conexões only where the person may configure an organization's", () => {
    expect(ids({}).includes("finance:/finance/plans")).toBe(true)
    expect(ids({}).includes("finance:/finance/settings/connections")).toBe(false)
    const org = {personal: false, canConfigure: true, canWrite: true}
    expect(ids({finance: org}).includes("finance:/finance/plans")).toBe(false)
    expect(ids({finance: org}).includes("finance:/finance/settings/connections")).toBe(true)
  })

  it("create actions follow the verbs: none for a viewer, no Nova conta without configure", () => {
    const viewer = {personal: true, canConfigure: false, canWrite: false}
    expect(ids({finance: viewer}).filter(i => i.startsWith("action:finance:"))).toEqual([])
    const writer = {personal: true, canConfigure: false, canWrite: true}
    const list = ids({finance: writer})
    expect(list).toContain("action:finance:recurrence")
    expect(list).not.toContain("action:finance:account")
  })

  it("Console create actions need an active tenant", () => {
    expect(ids({}).filter(i => i.startsWith("action:console:"))).toEqual(["action:console:price", "action:console:webhook"])
    expect(ids({console: {reviewer: false, active: false}}).filter(i => i.startsWith("action:console:"))).toEqual([])
  })

  it("an action asks to go to its screen with its kind; a page just carries its href", () => {
    const list = buildSearchEntries(base, t, go, "finance")
    list.find(e => e.id === "action:finance:recurrence")!.onSelect!()
    expect(go).toHaveBeenCalledWith("/finance/recurrences", "recurrence")
    expect(list.find(e => e.id === "finance:/finance/recurrences")).toMatchObject({kind: "page", href: "/finance/recurrences", group: "common.account.finance"})
  })
})
```

- [ ] **Step 2: Run** — Expected: FAIL (module not found).

- [ ] **Step 3: Implement** — `src/lib/search/entries.ts`

```ts
import type {CommandEntry} from "@aoctech/ui"

import {visibleFinanceSections} from "@/components/finance/FinanceNav"
import {consoleNav} from "@/lib/console/nav"
import type {CreateKind} from "@/lib/createRequest"
import {PORTAL_NAV} from "@/lib/portal/nav"

export type View = "portal" | "console" | "finance"

export interface SearchContext {
  /** Has a billing account (the portal session did not answer 403). */
  portal: boolean
  /** null: the person runs no organization. `active`: it has a tenant in this mode. */
  console: {reviewer: boolean; active: boolean} | null
  finance: {personal: boolean; canConfigure: boolean; canWrite: boolean}
}

export type SearchText = (key: string) => string
/** Navigate to `href`; with `kind`, ask the screen there to open its create form. */
export type SearchGo = (href: string, kind?: CreateKind) => void

/** Create actions: label key, kind, the screen that owns the form. */
const FINANCE_ACTIONS: {kind: CreateKind; labelKey: string; href: string; verb: "write" | "configure"}[] = [
  {kind: "bill", labelKey: "common.search.actions.bill", href: "/finance/bills", verb: "write"},
  {kind: "purchase", labelKey: "common.search.actions.purchase", href: "/finance/cards", verb: "write"},
  {kind: "recurrence", labelKey: "common.search.actions.recurrence", href: "/finance/recurrences", verb: "write"},
  {kind: "transfer", labelKey: "common.search.actions.transfer", href: "/finance/statement", verb: "write"},
  {kind: "account", labelKey: "common.search.actions.account", href: "/finance/accounts", verb: "configure"},
]
const CONSOLE_ACTIONS: {kind: CreateKind; labelKey: string; href: string}[] = [
  {kind: "price", labelKey: "common.search.actions.price", href: "/console/catalog"},
  {kind: "webhook", labelKey: "common.search.actions.webhook", href: "/console/webhooks"},
]

/** Keywords for a screen, from `common.search.keywords.<key>` ("a, b, c"); none when the catalog has no entry. */
function keywordsOf(t: SearchText, key: string): string[] {
  const path = `common.search.keywords.${key}`
  const raw = t(path)
  return raw && raw !== path ? raw.split(",").map(s => s.trim()).filter(Boolean) : []
}

export function buildSearchEntries(ctx: SearchContext, t: SearchText, go: SearchGo, current: View): CommandEntry[] {
  const section = (view: View): CommandEntry[] => {
    const group = t(`common.account.${view}`)
    const page = (id: string, label: string, href: string, keywordKey: string): CommandEntry => ({
      id: `${view}:${id}`, label, group, kind: "page", href, keywords: keywordsOf(t, keywordKey),
    })
    const action = (kind: CreateKind, labelKey: string, href: string): CommandEntry => ({
      id: `action:${view}:${kind}`, label: t(labelKey), group, kind: "action", keywords: keywordsOf(t, `action.${kind}`),
      onSelect: () => go(href, kind),
    })
    if (view === "portal") {
      if (!ctx.portal) return []
      return PORTAL_NAV.map(n => page(n.href, t(n.labelKey), n.href, `portal.${n.href.slice(1)}`))
    }
    if (view === "console") {
      if (!ctx.console?.active) return []
      const pages = consoleNav(true, ctx.console.reviewer).map(n => page(n.href, t(n.label), n.href, `console.${n.href.split("/")[2]}`))
      return [...pages, ...CONSOLE_ACTIONS.map(a => action(a.kind, a.labelKey, a.href))]
    }
    const pages = visibleFinanceSections(ctx.finance.personal, ctx.finance.canConfigure)
      .map(s => page(s.href, t(`finance.nav.${s.key}`), s.href, `finance.${s.key}`))
    const actions = FINANCE_ACTIONS
      .filter(a => (a.verb === "write" ? ctx.finance.canWrite : ctx.finance.canConfigure))
      .map(a => action(a.kind, a.labelKey, a.href))
    return [...pages, ...actions]
  }
  const order: View[] = [current, ...(["portal", "console", "finance"] as View[]).filter(v => v !== current)]
  return order.flatMap(section)
}
```

- [ ] **Step 4: Run** `npx vitest run src/lib/search/entries.test.ts` — Expected: PASS. (Key for Console pages is `console.<segment>`, e.g. `console.overview`; ids use the href so the test ids line up.)
- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(search): entries built from the three navs, by role and area"`

### Task 10: Hook, shell mount, copy

**Files:**
- Create: `src/lib/search/useSearchEntries.ts`
- Create: `src/components/shell/GlobalSearch.tsx`
- Modify: `src/components/shell/BillingShell.tsx` (`actions` slot and the palette)
- Modify: `src/locales/pt-BR/common.json`, `src/locales/en/common.json`
- Test: `src/components/shell/GlobalSearch.test.tsx`

**Interfaces:**
- Consumes: `buildSearchEntries`, `consoleKeys.session()`/`getConsoleSession`, `portalKeys.session`/`getSession`/`isNoBillingAccount`, `pickMerchantOrg`, `tenantInMode`, `useMerchantOrg`, `useMode`, `useFinanceSpaces`, `useSpace`, `requestCreate`, `CommandPalette`, `CommandPaletteTrigger`, `useCommandPaletteShortcut`.
- Produces: `useSearchEntries(view: View): CommandEntry[]`; `<GlobalSearch view={view}/>` (trigger + palette + shortcut).

- [ ] **Step 1: Copy** — add to `common.json` (pt-BR; the en file gets the English equivalents):

```json
"search": {
  "actions": {
    "bill": "Novo lançamento",
    "purchase": "Nova compra",
    "recurrence": "Nova recorrência",
    "transfer": "Nova transferência",
    "account": "Nova conta",
    "price": "Novo preço",
    "webhook": "Novo webhook"
  },
  "keywords": {
    "finance": {
      "bills": "contas a pagar, contas a receber, boletos, lançamentos",
      "statement": "saldo, movimentações, extrato",
      "cards": "cartão, fatura, compras",
      "recurrences": "recorrente, assinatura, fixo",
      "import": "ofx, csv, extrato bancário",
      "reports": "relatório, gráficos"
    },
    "console": {
      "invoices": "cobranças, boletos, pix",
      "catalog": "produtos, preços, planos",
      "webhooks": "eventos, endpoints, integração",
      "credentials": "api key, chave de api, token"
    },
    "portal": {"invoices": "faturas, pagar", "subscriptions": "assinaturas, planos"},
    "action": {"bill": "adicionar, criar, nova conta", "recurrence": "criar recorrente"}
  }
}
```
(English: "Search" actions "New bill", "New card purchase", "New recurrence", "New transfer", "New account", "New price", "New webhook"; keywords in English: bills "payables, receivables, invoices", statement "balance, transactions", cards "credit card, statement, purchases", recurrences "recurring, subscription", import "ofx, csv, bank statement", reports "charts", catalog "products, prices, plans", webhooks "events, endpoints, integration", credentials "api key, token".) Check `src/locales/*` has a key-parity test (`grep -rn "locales" src --include=*.test.*`); keep both files in lock-step.

- [ ] **Step 2: Failing test** — `src/components/shell/GlobalSearch.test.tsx`; mirror `AccountMenu.test.tsx` (mock `@/lib/auth/AuthContext`, `vi.spyOn(portal, "getSession")`, `vi.spyOn(consoleApi, "getConsoleSession")`, `renderWithQuery`, the finance mock via `serveFinanceMock()` for the spaces call). Mock `next/navigation`'s `useRouter` to a `push` spy and `usePathname` to `/finance`.

```tsx
it("opens on Ctrl+K and finds Recorrências and Nova recorrência for 'recor'", async () => {
  person({operator: false}); serveFinanceMock()
  renderWithQuery(<GlobalSearch view="finance"/>)
  fireEvent.keyDown(window, {key: "k", ctrlKey: true})
  await userEvent.type(await screen.findByRole("combobox"), "recor")
  const names = screen.getAllByRole("option").map(o => o.textContent)
  expect(names[0]).toContain("Recorrências")
  expect(names[1]).toContain("Nova recorrência")
})

it("choosing the action asks the screen to open its form and navigates there", async () => {
  /* same setup */
  await userEvent.click(await screen.findByRole("option", {name: /Nova recorrência/}))
  expect(push).toHaveBeenCalledWith("/finance/recurrences")
  expect(takePendingCreate("recurrence")).toBe(true)
})

it("offers Console pages to an operator and none to someone without an organization", async () => { /* person({operator:true|false}); assert "Faturas"/"Catálogo" present/absent with group "Console" */ })

it("a page in another subproduct is a link to its route", async () => {
  /* view="finance", operator → option name /Catálogo/ has href "/console/catalog" */
})

it("a person with no billing account has no Portal entries", async () => {
  vi.spyOn(portal, "getSession").mockRejectedValue(forbidden) // as AccountMenu.test's 403
  /* open, type "assinaturas": only the Console/Finanças matches, none grouped "Portal" */
})
```
Fill each with the setup shown in the first test; assert exactly what each title states.

- [ ] **Step 3: Run** — Expected: FAIL (module not found).

- [ ] **Step 4: Implement the hook** — `src/lib/search/useSearchEntries.ts`

```ts
"use client"

import type {CommandEntry} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {useRouter} from "next/navigation"
import {useMemo} from "react"
import {useTranslation} from "react-i18next"

import {isNoBillingAccount} from "@/lib/api/client"
import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {getSession, portalKeys} from "@/lib/api/portal"
import {useAuth} from "@/lib/auth/AuthContext"
import {pickMerchantOrg, tenantInMode} from "@/lib/console/merchant"
import {useMerchantOrg} from "@/lib/console/useMerchantOrg"
import {useMode} from "@/lib/console/useMode"
import {useSpace} from "@/lib/console/useSpace"
import {type CreateKind, requestCreate} from "@/lib/createRequest"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {buildSearchEntries, type View} from "@/lib/search/entries"

/**
 * What this person can reach, from the queries the account menu already runs
 * (same keys: no extra request). A subproduct whose access is still unknown
 * contributes nothing, except the one on screen, which is known by being there.
 */
export function useSearchEntries(view: View): CommandEntry[] {
  const {t} = useTranslation()
  const router = useRouter()
  const {authenticated} = useAuth()
  const mode = useMode()
  const stored = useMerchantOrg()
  const space = useSpace()
  const {can} = useFinanceSpaces()

  const portalSession = useQuery({
    queryKey: portalKeys.session, queryFn: getSession, enabled: authenticated, retry: false, retryOnMount: false,
    refetchOnWindowFocus: q => !isNoBillingAccount(q.state.error),
  })
  const consoleSession = useQuery({
    queryKey: consoleKeys.session(), queryFn: getConsoleSession, enabled: authenticated, retry: false,
    staleTime: 5 * 60_000, refetchOnWindowFocus: false,
  })

  const portal = view === "portal" ? !isNoBillingAccount(portalSession.error) : portalSession.isSuccess
  const orgs = consoleSession.data?.organizations ?? []
  const selected = consoleSession.data && orgs.length > 0 ? pickMerchantOrg(orgs, stored, mode) : null
  const consoleCtx = selected ? {active: tenantInMode(selected.merchant_status, mode), reviewer: selected.can_review_applications === true} : null
  const personal = space.kind === "personal"
  const canConfigure = can("finance.configure")
  const canWrite = can("finance.write")

  return useMemo(() => {
    const go = (href: string, kind?: CreateKind) => {
      if (kind) requestCreate(kind, href)
      router.push(href)
    }
    return buildSearchEntries({portal, console: consoleCtx, finance: {personal, canConfigure, canWrite}}, key => t(key), go, view)
    // consoleCtx is rebuilt each render; its two fields are the real inputs.
  }, [portal, consoleCtx?.active, consoleCtx?.reviewer, personal, canConfigure, canWrite, view, t, router]) // eslint-disable-line react-hooks/exhaustive-deps
}
```
(If `pickMerchantOrg`/`tenantInMode` signatures differ from the console layout's usage `pickMerchantOrg(organizations, stored, mode)` / `tenantInMode(selected.merchant_status, mode)`, follow the layout — it is the reference.) `useMemo` here is for `entries` identity so the palette's `useMemo` over `entries` holds; with the React Compiler on, drop the manual memo and the eslint-disable if lint says so.

- [ ] **Step 5: Implement the shell piece** — `src/components/shell/GlobalSearch.tsx`

```tsx
"use client"

import {CommandPalette, CommandPaletteTrigger, useCommandPaletteShortcut} from "@aoctech/ui"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {renderLink} from "@/components/shell/BillingShell"
import {useSearchEntries} from "@/lib/search/useSearchEntries"
import type {View} from "@/lib/search/entries"

/** The bar's search button and the palette it opens (⌘K / Ctrl+K from anywhere). */
export function GlobalSearch({view}: {view: View}) {
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
```
`renderLink` lives in `BillingShell.tsx`, which will import `GlobalSearch` → circular import. Break it by moving `renderLink` and `ShellView` into `src/components/shell/renderLink.tsx` (re-exported from `BillingShell.tsx` so the existing `import {renderLink} from "@/components/shell/BillingShell"` sites keep working) and importing it from there in `GlobalSearch.tsx`.

In `BillingShell.tsx`: `actions={<><GlobalSearch view={view}/><LanguageSwitcher className="max-sm:hidden"/></>}`. Update the component's doc comment (the bar now carries the search).

- [ ] **Step 6: Run** `npx vitest run src/components/shell src/lib/search src/components/AccountMenu.test.tsx` — Expected: PASS; then `npx tsc --noEmit && npm run lint`.
- [ ] **Step 7: Commit** — `git add -A && git commit -m "feat(search): ⌘K across portal, console and Finanças"`

### Task 11: Docs, browser check, PR

**Files:**
- Modify: `ui/DESIGN.md` (shell section: the search button in the bar, the palette, the create-request wire), `docs/specs/2026-10-14-global-search-and-portal-width.md` (§3.3: "built on Base UI's Dialog directly, as Drawer does, since Modal carries a title bar and footer" — the one deviation from the spec text)

- [ ] **Step 1: Browser check** (`npm run dev`, chrome-devtools; mock data as in Task 7):
  1. Portal 1440px and 390px: no fixed column, no horizontal scroll.
  2. Ctrl+K in each area; the button in the bar at 390px is icon-only and ≥44px.
  3. Type "recor": "Recorrências", then "Nova recorrência"; Enter on the second lands on `/finance/recurrences` with the drawer open; reload the page: the drawer is **not** open.
  4. From Finanças search "catálogo": lands in the Console, the shell is the Console's; then "assinaturas" to the Portal's.
  5. As a person with no organization: no Console results. Keyboard only: Tab to the button, Enter, type, arrows, Enter, Esc.
  Screenshot each; fix anything off before the PR.
- [ ] **Step 2: Full gate** — `npm run lint && npx tsc --noEmit && npm test && npm run build` (static export) — Expected: all green.
- [ ] **Step 3: Docs commit** — `git add -A && git commit -m "docs(ui): the search in the shell"`
- [ ] **Step 4: PR** — `git push -u origin feat/global-search && gh pr create` (title `feat(ui): ⌘K search across the three areas; portal at full width`; body: what/why, the decision to keep `requestCreate` instead of a query parameter, no infra cost; no attribution lines). Merge when CI is green.

---

## Self-review

- **Spec coverage:** §2 Portal width → Task 7; §3.1–3.3 API/matching/behaviour → Tasks 1–4; §3.4 package work → Tasks 2, 5; §4.1 entries and gating → Tasks 7 (PORTAL_NAV), 9; §4.2 create actions and the `createRequest` move → Task 6, entries in Task 9; §4.3 mounting → Task 10; §4.4 i18n/docs → Tasks 10, 11; §5 delivery order → Parts A/B with the release gate; §6 tests → in each task plus the browser check. The one deviation (Dialog directly, not `Modal`) is recorded in Task 11.
- **Type consistency:** `CommandEntry` (Task 3) is what `buildSearchEntries` returns (Task 9) and what `useSearchEntries` returns (Task 10); `CreateKind` gains `price|webhook` in Task 6 before Task 9 uses them; `View` is exported from `entries.ts` and used by the hook and `GlobalSearch`; entry ids (`<view>:<href>`, `action:<view>:<kind>`) match between Task 9's code and tests.
- **Placeholders:** the Task 10 test bodies beyond the first two name their setup and assertions explicitly; the Task 7 visual fixes are inherently discovered in the browser and are bounded by the stated rules (prose measure, form width, grid).

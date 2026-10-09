"use client"

import {useQuery} from "@tanstack/react-query"
import {useEffect, useMemo} from "react"
import {toast} from "sonner"

import {type FinanceCtx, financeKeys, getFinanceSpaces} from "@/lib/api/finance"
import type {FinanceSpaceEntry, Verb} from "@/lib/api/financeTypes"
import {parseSpace, PERSONAL, sameSpace, setSpace, type Space} from "@/lib/console/space"
import {t} from "@/lib/i18n"
import {useMode} from "@/lib/console/useMode"
import {useSpace} from "@/lib/console/useSpace"

/** The (mode, space) every finance call and query key is made with. */
export function useFinanceCtx(): FinanceCtx {
  const mode = useMode()
  const space = useSpace()
  return useMemo(() => ({mode, space}), [mode, space])
}

/**
 * The selector the server named for an entry. The console's `Space` is the
 * X-Billing-Space grammar ("personal" | "org:{id}"), so a personal workspace is
 * an `org:` selector like an organization; its kind is on the entry.
 */
function entrySpace(e: FinanceSpaceEntry): Space {
  return parseSpace(e.selector)
}

/** "Pessoal" in the reader's language; every other space by its own name. */
function labelOf(e: FinanceSpaceEntry): string {
  return e.kind === "personal_default" ? t("finance.space.personal") : e.display_name
}

/**
 * The spaces the server says this person may pick, and the current one.
 *
 * The stored selection is re-validated against the list every time it loads:
 * an organization the person no longer belongs to falls back to personal, once,
 * with a notice — instead of every screen answering 404. When ctech-account is
 * unreachable the list holds only personal; a stored organization is then kept
 * (its requests will say the space is unavailable) rather than silently dropped.
 */
export function useFinanceSpaces() {
  const space = useSpace()
  const q = useQuery({queryKey: financeKeys.spaces(), queryFn: getFinanceSpaces, staleTime: 60_000})
  const spaces = q.data?.spaces ?? []
  const unavailable = q.data?.organizations_unavailable ?? false
  const current = spaces.find(e => sameSpace(entrySpace(e), space))

  useEffect(() => {
    if (!q.data || unavailable || current || space.kind === "personal") return
    setSpace(PERSONAL)
    toast.info(t("bills.spaces.lost"))
  }, [q.data, unavailable, current, space])

  const verbs = new Set<Verb>(current?.verbs ?? [])
  return {
    spaces,
    current,
    unavailable,
    loading: q.isLoading,
    error: q.error,
    can: (verb: Verb) => verbs.has(verb),
    spaceOf: entrySpace,
    labelOf,
  }
}

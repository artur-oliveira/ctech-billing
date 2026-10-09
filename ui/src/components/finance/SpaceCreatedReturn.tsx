"use client"

import {useQueryClient} from "@tanstack/react-query"
import {useRouter, useSearchParams} from "next/navigation"
import {useEffect, useRef} from "react"
import {useTranslation} from "react-i18next"

import {financeKeys, getFinanceSpaces} from "@/lib/api/finance"
import {FINANCE_HREF} from "@/lib/console/nav"
import {parseSpace, setSpace} from "@/lib/console/space"
import {readCreatedReturn} from "@/lib/finance/spaceHandoff"

/**
 * The return leg of "Novo espaço" (spec § 5.2). Reads the state once, reloads
 * the spaces list from the server, selects the new space ONLY if that list
 * holds it, and goes to Finanças whatever happened: a discarded or cancelled
 * return is silent, and the switcher shows the rest.
 */
export function SpaceCreatedReturn() {
  const {t} = useTranslation()
  const router = useRouter()
  const params = useSearchParams()
  const qc = useQueryClient()
  // The state is single-use; a second effect run (Strict Mode) must not read
  // it again and discard what the first run accepted.
  const done = useRef(false)

  useEffect(() => {
    if (done.current) return
    done.current = true
    const r = readCreatedReturn(new URLSearchParams(params.toString()))
    void (async () => {
      if (r.kind === "created") {
        try {
          const list = await qc.fetchQuery({queryKey: financeKeys.spaces(), queryFn: getFinanceSpaces, staleTime: 0})
          const selector = `org:${r.organizationId}`
          if (list.spaces.some(e => e.selector === selector)) setSpace(parseSpace(selector))
        } catch {
          // The list could not be read: nothing is selected, and the switcher
          // says why when Finanças opens.
        }
      }
      router.replace(FINANCE_HREF)
    })()
  }, [params, qc, router])

  return <p className="text-sm text-muted-foreground">{t("finance.space.returning")}</p>
}

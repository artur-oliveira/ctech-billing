"use client"

import {useRouter} from "next/navigation"
import {useEffect} from "react"

/**
 * Finanças lived at /console/finance/* until it became its own area. A
 * bookmark, a shared link, or ctech-account's handoff returning to the old
 * path lands here and is moved to the same place under /finance, with its
 * query (a card, a bill, the handoff's state) intact.
 */
export function legacyFinanceTarget(pathname: string, search: string): string {
  const rest = pathname.replace(/^\/console\/finance/, "").replace(/\/$/, "")
  return "/finance" + rest + search
}

export function LegacyFinanceRedirect() {
  const router = useRouter()
  useEffect(() => {
    router.replace(legacyFinanceTarget(window.location.pathname, window.location.search))
  }, [router])
  return null
}

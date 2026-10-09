"use client"

import {useQuery} from "@tanstack/react-query"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {useAuth} from "@/lib/auth/AuthContext"

/**
 * The portal's way into the console — for everyone now.
 *
 * Every signed-in person has a personal finance space (ADR 0025), so the
 * console is no longer only for operators. The session probe still decides
 * WHERE the link lands: an operator on invoicing's overview, everybody else
 * on Finanças. A failed probe is not an error; the link then opens Finanças,
 * which needs no organization.
 */
export function ConsoleLink() {
  const {t} = useTranslation()
  const {authenticated} = useAuth()

  const {data} = useQuery({
    // Live: the portal has no mode switch, and the console opens on live too.
    queryKey: consoleKeys.session("live"),
    queryFn: () => getConsoleSession("live"),
    enabled: authenticated,
    retry: false,
    // An operator does not gain or lose an organization mid-session, so this is
    // asked once and left alone.
    staleTime: Infinity,
  })

  if (!authenticated) return null

  return (
    <Link
      href={data ? "/console/overview" : "/console/finance"}
      className="shrink-0 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      {t("portal.nav.console")}
    </Link>
  )
}

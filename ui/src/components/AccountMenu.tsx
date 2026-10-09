"use client"

import {type RenderLink, UserMenu} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {LayoutGrid, Receipt} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {getSession, portalKeys} from "@/lib/api/portal"
import {useAuth} from "@/lib/auth/AuthContext"

const renderLink: RenderLink = props => <Link {...props}/>

/**
 * The signed-in person, in both shells: who they are (the whole name and the
 * e-mail, never cut), which of the two views they are in, and Sair. The one
 * place the portal and the console meet, so switching between them happens
 * here and nowhere else.
 *
 * Console is offered to everyone signed in: every person has a personal
 * finance space (ADR 0025), so there is nobody the console would answer 403.
 * The operator probe (the console session, live) decides only WHERE it opens:
 * an operator on invoicing's overview, everybody else on Finanças. A failed
 * probe is not an error; it means Finanças.
 *
 * The name comes from the portal session, which has the full name and the
 * e-mail; with no billing account behind it, the id_token's name is used and
 * there is no e-mail to show.
 */
export function AccountMenu({view}: {view: "portal" | "console"}) {
  const {t, i18n} = useTranslation()
  const {name: signedInName, authenticated, logout} = useAuth()

  const {data: session} = useQuery({
    queryKey: portalKeys.session,
    queryFn: getSession,
    enabled: authenticated,
    retry: false,
  })
  const {data: operator} = useQuery({
    // Live: the portal has no mode switch, and the console opens on live too.
    queryKey: consoleKeys.session("live"),
    queryFn: () => getConsoleSession("live"),
    enabled: authenticated,
    retry: false,
    // An operator does not gain or lose an organization mid-session.
    staleTime: Infinity,
  })

  if (!authenticated) return null

  return (
    <UserMenu
      name={session?.name || signedInName || t("common.account.fallbackName")}
      email={session?.email}
      locale={i18n.language}
      renderLink={renderLink}
      // 32px in the compact console is for a mouse; a finger gets 44.
      className="touch:size-11"
      views={[
        {label: t("common.account.portal"), icon: <Receipt/>, href: "/dashboard", current: view === "portal"},
        {
          label: t("common.account.console"),
          icon: <LayoutGrid/>,
          href: operator ? "/console/overview" : "/console/finance",
          current: view === "console",
        },
      ]}
      onSignOut={logout}
    />
  )
}

"use client"

import {type RenderLink, UserMenu} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {LayoutGrid, Receipt, Wallet} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {isNoBillingAccount} from "@/lib/api/client"
import {getSession, portalKeys} from "@/lib/api/portal"
import {useAuth} from "@/lib/auth/AuthContext"
import {FINANCE_HREF} from "@/lib/finance/href"

const renderLink: RenderLink = props => <Link {...props}/>

/**
 * The signed-in person, in all three areas: who they are (the whole name and
 * the e-mail, never cut), which area they are in, and Sair. The one place the
 * portal, the console and Finanças meet, so switching between them happens
 * here and nowhere else.
 *
 * Portal and Finanças are everyone's (everybody is a customer and has a
 * personal space, ADR 0025). Console is invoicing, so it is offered only when
 * the operator probe (the console session, live) answers; a failed probe is
 * not an error, it means no console.
 *
 * The name comes from the portal session, which has the full name and the
 * e-mail; with no billing account behind it, the id_token's name is used and
 * there is no e-mail to show.
 */
export function AccountMenu({view}: {view: "portal" | "console" | "finance"}) {
  const {t, i18n} = useTranslation()
  const {name: signedInName, authenticated, logout} = useAuth()

  const {data: session} = useQuery({
    queryKey: portalKeys.session,
    queryFn: getSession,
    enabled: authenticated,
    retry: false,
    // The portal shell mounts this menu BECAUSE the session answered 403 (no
    // billing account). Asking again on mount would clear that error, unmount
    // the empty state and this menu, get the 403 back, and loop forever.
    retryOnMount: false,
    refetchOnWindowFocus: query => !isNoBillingAccount(query.state.error),
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
        // Only for an operator: the console is invoicing, and somebody with no
        // organization has nothing there.
        ...(operator || view === "console"
          ? [{label: t("common.account.console"), icon: <LayoutGrid/>, href: "/console/overview", current: view === "console"}]
          : []),
        {label: t("common.account.finance"), icon: <Wallet/>, href: FINANCE_HREF, current: view === "finance"},
      ]}
      onSignOut={logout}
    />
  )
}

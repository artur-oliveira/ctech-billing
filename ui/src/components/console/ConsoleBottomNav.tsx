"use client"

import {BottomNav, type RenderLink} from "@aoctech/ui"
import {Building2, Ellipsis, FileText, House, Package, Users, Webhook, CreditCard, Settings, KeyRound, Landmark, Gauge} from "lucide-react"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

import {consoleNav} from "@/lib/console/nav"

const renderLink: RenderLink = props => <Link {...props}/>

export function ConsoleBottomNav({hasOrganization, reviewer}: {hasOrganization: boolean; reviewer: boolean}) {
  const {t, i18n} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const nav = consoleNav(hasOrganization, reviewer)
  const current = (href: string) => pathname === href || (href !== "/console/overview" && pathname.startsWith(href))
  const moreItems = [
    ["/console/customers", "customers", <Users key="customers"/>],
    ["/console/catalog", "catalog", <Package key="catalog"/>],
    ["/console/connections", "connections", <Landmark key="connections"/>],
    ["/console/plan", "plan", <Gauge key="plan"/>],
    ["/console/credentials", "credentials", <KeyRound key="credentials"/>],
    ["/console/webhooks", "webhooks", <Webhook key="webhooks"/>],
    ["/console/settings", "settings", <Settings key="settings"/>],
    ["/console/applications", "applications", <Building2 key="applications"/>],
  ] as const

  return <BottomNav
    renderLink={renderLink}
    locale={i18n.language}
    action={{label: t("console.nav.overview"), icon: <House/>, onClick: () => router.push("/console/overview")}}
    items={[
      {label: t("console.nav.invoices"), icon: <FileText/>, href: "/console/invoices", active: current("/console/invoices")},
      {label: t("console.nav.subscriptions"), icon: <CreditCard/>, href: "/console/subscriptions", active: current("/console/subscriptions")},
      {
        type: "more",
        label: t("console.nav.more"),
        title: t("console.nav.moreTitle"),
        icon: <Ellipsis/>,
        items: moreItems.filter(([href]) => nav.some(item => item.href === href)).map(([href, key, icon]) => ({
          label: t(`console.nav.${key}`), icon, href, active: current(href), group: t("console.nav.groupMore"),
        })),
      },
    ]}
  />
}

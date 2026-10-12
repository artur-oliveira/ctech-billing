"use client"

import {BottomNav, type RenderLink} from "@aoctech/ui"
import {FileText, House, Repeat} from "lucide-react"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

const renderLink: RenderLink = props => <Link {...props}/>

export function PortalBottomNav({hidden = false}: {hidden?: boolean}) {
  const {t, i18n} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const active = (href: string) => href === "/dashboard" ? pathname === href : pathname.startsWith(href)

  if (hidden) return null

  return <BottomNav
    renderLink={renderLink}
    locale={i18n.language}
    action={{label: t("portal.nav.home"), icon: <House/>, onClick: () => router.push("/dashboard")}}
    items={[
      {label: t("portal.nav.invoices"), icon: <FileText/>, href: "/invoices", active: active("/invoices")},
      {label: t("portal.nav.subscriptions"), icon: <Repeat/>, href: "/subscriptions", active: active("/subscriptions")},
    ]}
  />
}

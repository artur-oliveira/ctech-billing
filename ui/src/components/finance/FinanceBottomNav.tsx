"use client"

import {BottomNav, type BottomNavAction, type RenderLink} from "@aoctech/ui"
import {
  ArrowLeftRight, ChartColumn, CreditCard, Ellipsis, House, Landmark, Plus, Receipt, Repeat, ScrollText, Upload,
} from "lucide-react"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useEffect} from "react"
import {useTranslation} from "react-i18next"

import {currentFinanceSection} from "@/components/finance/FinanceNav"
import type {Verb} from "@/lib/api/financeTypes"
import {type CreateKind, dropCreateUnlessAt, requestCreate} from "@/lib/finance/createRequest"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"

const BASE = "/console/finance"
const BILLS = `${BASE}/bills`

const renderLink: RenderLink = props => <Link {...props}/>

/**
 * What the central button creates on each screen: the thing that screen
 * lists, opened in the screen's own drawer. A screen with nothing of its own
 * to create (Resumo, Importar, Relatórios) adds a lançamento, the most
 * frequent write in Finanças, and goes to A pagar/receber to do it, so the new
 * line is on screen once it is saved.
 */
const CREATE: Record<string, {kind: CreateKind; verb: Verb}> = {
  [BILLS]: {kind: "bill", verb: "finance.write"},
  [`${BASE}/cards`]: {kind: "purchase", verb: "finance.write"},
  [`${BASE}/recurrences`]: {kind: "recurrence", verb: "finance.write"},
  [`${BASE}/statement`]: {kind: "transfer", verb: "finance.write"},
  [`${BASE}/accounts`]: {kind: "account", verb: "finance.configure"},
}
const FALLBACK = {kind: "bill" as const, verb: "finance.write" as const}

const ACTION_ICON: Record<CreateKind, React.ReactNode> = {
  bill: <Plus/>,
  purchase: <Plus/>,
  recurrence: <Plus/>,
  transfer: <ArrowLeftRight/>,
  account: <Plus/>,
}

/**
 * Finanças on a phone: a bar at the bottom, where the thumb is, instead of the
 * laptop's section list squeezed into a picker. Hidden from `md` by the
 * component itself, where FinanceNav takes over.
 *
 * Three destinations a person opens daily and Mais for the rest. The central
 * slot is always "create", and what it creates follows the screen; a role that
 * may not create there gets no slot at all rather than a button that fails.
 */
export function FinanceBottomNav() {
  const {t, i18n} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const {can} = useFinanceSpaces()
  const current = currentFinanceSection(pathname)
  const is = (href: string) => current === href
  // Resumo's Adicionar is bound for A pagar/receber; landing anywhere else
  // first (a tap on Extrato mid-navigation) abandons it.
  useEffect(() => {
    dropCreateUnlessAt(current)
  }, [current])

  const own = CREATE[current]
  const create = own ?? FALLBACK
  const action: BottomNavAction | null = can(create.verb)
    ? {
      label: t(`finance.nav.create.${create.kind}`),
      icon: ACTION_ICON[create.kind],
      onClick: () => {
        if (own) {
          requestCreate(create.kind)
        } else {
          requestCreate(create.kind, BILLS)
          router.push(BILLS)
        }
      },
    }
    : null

  const sheet = (href: string, key: string, icon: React.ReactNode, group: string) => ({
    label: t(`finance.nav.${key}`), icon, href, active: is(href), group,
  })
  const launch = t("finance.nav.groupLaunch")
  const manage = t("finance.nav.groupManage")

  return (
    <BottomNav
      renderLink={renderLink}
      locale={i18n.language}
      action={action}
      items={[
        {label: t("finance.nav.overview"), icon: <House/>, href: BASE, active: is(BASE)},
        {label: t("finance.nav.billsShort"), icon: <Receipt/>, href: BILLS, active: is(BILLS)},
        {label: t("finance.nav.statement"), icon: <ScrollText/>, href: `${BASE}/statement`, active: is(`${BASE}/statement`)},
        {
          type: "more",
          label: t("finance.nav.more"),
          title: t("finance.nav.moreTitle"),
          icon: <Ellipsis/>,
          items: [
            sheet(`${BASE}/recurrences`, "recurrences", <Repeat/>, launch),
            sheet(`${BASE}/cards`, "cards", <CreditCard/>, launch),
            sheet(`${BASE}/import`, "import", <Upload/>, launch),
            sheet(`${BASE}/reports`, "reports", <ChartColumn/>, manage),
            sheet(`${BASE}/accounts`, "accounts", <Landmark/>, manage),
          ],
        },
      ]}
    />
  )
}

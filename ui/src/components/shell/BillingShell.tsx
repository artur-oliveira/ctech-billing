"use client"

import {AppShell, Badge, DensityScope, type RenderLink} from "@aoctech/ui"
import Image from "next/image"
import Link from "next/link"
import type {ReactNode} from "react"
import {useTranslation} from "react-i18next"

import {AccountMenu} from "@/components/AccountMenu"

export const renderLink: RenderLink = props => <Link {...props}/>

export type ShellView = "portal" | "console" | "finance"

/**
 * The frame of the three areas: @aoctech/ui's AppShell, one column at the left
 * edge from `md` (the mark, whose work, the sections, the person) and the page
 * across the rest of the width. Under `md` the column is the bar across the
 * top and the sections are the area's BottomNav.
 *
 * The column is compact in every area, so the organization, space and
 * account controls are the same 32px in the portal as in the console; the
 * page keeps its own density (`density`).
 *
 * The name is the product's, CTech Billing, and the area follows it after a
 * bullet: "CTech Billing • Finanças". Finanças is a part of Billing, not
 * another product.
 */
export function BillingShell({
  view,
  home,
  context,
  nav,
  banner,
  bottomNav,
  density,
  contentClassName,
  children,
}: {
  view: ShellView
  home: string
  context?: ReactNode
  /** The sections, a SidebarNav (FinanceNav, or the console's groups). */
  nav?: ReactNode
  banner?: ReactNode
  bottomNav?: ReactNode
  density: "compact" | "comfortable"
  contentClassName?: string
  children: ReactNode
}) {
  const {t, i18n} = useTranslation()
  return (
    <DensityScope density="compact">
      <AppShell
        locale={i18n.language}
        brand={<Wordmark href={home} area={t(`common.account.${view}`)}/>}
        context={context}
        nav={nav}
        account={<AccountMenu view={view} trigger="row"/>}
        banner={banner}
        bottomNav={bottomNav}
        contentClassName={contentClassName}
      >
        <DensityScope density={density}>{children}</DensityScope>
      </AppShell>
    </DensityScope>
  )
}

/** The mark, "CTech Billing" and the area. On a phone the mark alone: the bar needs the room. */
function Wordmark({href, area}: {href: string; area: string}) {
  return (
    <Link href={href} className="flex min-w-0 items-center gap-2.5 rounded-md outline-none focus-visible:ring-3 focus-visible:ring-ring/50 touch:min-h-11">
      <Image src="/android-chrome-192x192.png" alt="" width={24} height={24} className="size-6 shrink-0 rounded-md"/>
      <span className="truncate text-sm tracking-[-0.02em] max-sm:sr-only">
        <span className="font-semibold text-brand-600">CTech Billing</span>
        <span aria-hidden className="mx-1.5 text-muted-foreground">•</span>
        <span className="text-muted-foreground">{area}</span>
      </span>
    </Link>
  )
}

/** The test-mode band, the loudest thing on the page after the danger colour. */
export function TestBanner({children}: {children: ReactNode}) {
  return (
    <p className="z-20 border-b border-warning/30 bg-[color-mix(in_oklab,var(--color-warning)_12%,var(--color-background))] px-4 py-2 text-center text-xs text-foreground md:sticky md:top-0">
      {children}
    </p>
  )
}

/**
 * Whose work, and in which mode: the switch fills the row, and in test mode a
 * "Teste" mark sits beside it, so the answer to "which world am I in" stays on
 * screen while the switch itself lives in the account menu.
 */
export function ShellContext({children, test, testLabel}: {children: ReactNode; test: boolean; testLabel: string}) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <div className="min-w-0 flex-1">{children}</div>
      {test && <Badge tone="attention" className="shrink-0">{testLabel}</Badge>}
    </div>
  )
}

"use client"

import {BottomNavSpacer, Button, DensityScope, ErrorState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import Image from "next/image"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useEffect} from "react"
import {useTranslation} from "react-i18next"

import {AccountMenu} from "@/components/AccountMenu"
import {LanguageSwitcher} from "@/components/LanguageSwitcher"
import {ModeSwitch} from "@/components/console/ModeSwitch"
import {MerchantInvitation} from "@/components/console/MerchantInvitation"
import {MerchantStatusBand} from "@/components/console/MerchantStatusBand"
import {OrganizationSwitch} from "@/components/console/OrganizationSwitch"
import {ConsoleBottomNav} from "@/components/console/ConsoleBottomNav"
import {SectionNav} from "@/components/navigation/SectionNav"
import {statusOf} from "@/lib/api/client"
import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {useAuth} from "@/lib/auth/AuthContext"
import {pickMerchantOrg, setMerchantOrg, tenantInMode} from "@/lib/console/merchant"
import {consoleNav} from "@/lib/console/nav"
import {ConsoleScopeProvider} from "@/lib/console/scope"
import {MerchantSelectionProvider} from "@/lib/console/selection"
import {useMerchantOrg} from "@/lib/console/useMerchantOrg"
import {useMode} from "@/lib/console/useMode"
import {FINANCE_HREF} from "@/lib/finance/href"

/**
 * The operator shell — invoicing, the second of the three areas the app ships
 * (portal, console, Finanças), and the same components as the first at a
 * different density.
 *
 * `<DensityScope density="compact">` is the whole of that difference: every
 * control from `@aoctech/ui` reads its `data-density` and sizes itself to 32px
 * instead of 44, and the drawers, modals, selects and menus opened from here
 * (portaled, outside this element) follow the scope too, on a desktop as well
 * (UX batch 5; under touch @aoctech/ui's touch.css draws them 36px with a 44px
 * target). An operator
 * works in this all day and wants rows on screen; the same person opens the
 * portal twice a month on a phone and wants a thumb target. Neither is a prop
 * a call site remembers to pass.
 *
 * Wider than the portal, too: `max-w-6xl` against `max-w-2xl`. The portal reads
 * one bill; this reads a table.
 *
 * The mode is in the header and never anywhere else. It is the one thing an
 * operator switches that changes which world the numbers come from, and acting
 * on the wrong one is the expensive mistake. The organization is switchable: it
 * is the selected account organization where the person is owner or admin
 * (ADR 0028), a request the server re-authorizes on every call; the mode is the
 * other switch.
 */
export default function ConsoleLayout({children}: LayoutProps<"/console">) {
  const {t, i18n} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const {authenticated, loading} = useAuth()
  const mode = useMode()
  const stored = useMerchantOrg()
  const {data: session, error, isPending, refetch} = useQuery({
    queryKey: consoleKeys.session(),
    queryFn: getConsoleSession,
    enabled: authenticated,
    retry: false,
    staleTime: 60_000,
  })

  const organizations = session?.organizations ?? []
  const unavailable = session?.organizations_unavailable ?? false
  const selected = session ? pickMerchantOrg(organizations, stored, mode) : null
  const active = selected !== null && tenantInMode(selected.merchant_status, mode)
  const org = selected?.organization_id
  // Stable per (mode, organization): the React Compiler memoizes it.
  const scope = org ? {mode, org} : null
  // The selected organization with or without a tenant in this mode: the
  // invitation, the apply form and the status band read it.
  const selection = selected && scope ? {scope, organization: selected} : null
  const onApply = pathname.startsWith("/console/apply")
  // Bank connections need no tenant (BYOK spec § 3.1): an organization may
  // connect for its statement before it bills through CTech.
  const onConnections = pathname.startsWith("/console/connections")
  const reviewer = selected?.can_review_applications === true

  // Owner or admin of no organization: nothing here is theirs; Finanças is.
  const noOrganization = statusOf(error) === 403 || (session !== undefined && !unavailable && organizations.length === 0)

  // A stored organization the person no longer administers is replaced once.
  useEffect(() => {
    if (org && org !== stored) setMerchantOrg(org)
  }, [org, stored])

  useEffect(() => {
    if (!loading && !authenticated) router.replace("/login")
  }, [loading, authenticated, router])

  useEffect(() => {
    if (noOrganization) router.replace(FINANCE_HREF)
  }, [noOrganization, router])

  const nav = consoleNav(active, reviewer)

  return (
    <DensityScope density="compact" className="min-h-dvh">
      <header className="border-b border-border">
        <div className="mx-auto flex min-h-14 max-w-6xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2">
          <div className="order-1 flex min-w-0 items-center gap-4">
            <Link href="/console/overview" className="flex shrink-0 items-center gap-2.5 touch:min-h-11">
              <Image
                src="/android-chrome-192x192.png"
                alt=""
                width={24}
                height={24}
                className="size-6 rounded-md"
              />
              <span className="text-sm font-semibold tracking-[-0.02em] text-brand-600">
                CTech
                <span className="ml-1.5 font-normal text-muted-foreground">Billing</span>
              </span>
            </Link>
          </div>

          <div className="order-3 flex w-full min-w-0 items-center gap-x-3 gap-y-2 sm:order-2 sm:ml-auto sm:w-auto">
            {/* Whose billing, then which world: "CTech · Teste" reads as one thing. */}
            {selected && <OrganizationSwitch organizations={organizations} selected={selected}/>}
            <ModeSwitch/>
          </div>
          {/* The person, the way to the portal, and Sair, all behind the
              avatar: the same person holds both shells, and the switch
              between them happens in one place. */}
          <div className="order-2 ml-auto flex items-center gap-2 sm:order-3 sm:ml-0">
            <LanguageSwitcher/>
            <AccountMenu view="console"/>
          </div>
        </div>

      </header>

      {/* The test-mode band. Deliberately the loudest thing on the page after
          the danger colour: every number below it is fake, and an operator who
          forgets that voids a real invoice believing it is a sandbox one. */}
      {mode === "test" && (
        <p className="bg-warning/12 border-b border-warning/30 px-4 py-2 text-center text-xs text-foreground">
          {t("console.shell.testBanner")}
        </p>
      )}
      {selection && !onApply && (
        <MerchantSelectionProvider value={selection}><MerchantStatusBand/></MerchantSelectionProvider>
      )}

      <main className="mx-auto grid max-w-6xl gap-6 px-4 py-8 pb-20 lg:grid-cols-[11rem_minmax(0,1fr)] lg:gap-10">
        {active && <aside className="max-md:hidden lg:sticky lg:top-6 lg:self-start">
          <SectionNav items={nav.map(item => ({href: item.href, label: item.label, exact: item.href === "/console/overview"}))} label={t("console.nav.label")} selectLabel={t("console.nav.label")}/>
        </aside>}
        <div className="min-w-0">
        {loading || !authenticated || noOrganization || (session === undefined && isPending) ? (
          <ShellSkeleton/>
        ) : unavailable || (error && !session) ? (
          // ctech-account did not answer, or the session itself failed: the
          // list is unknown, not empty, so nothing is chosen and nobody is
          // sent away. One retry, in the console's words.
          <ErrorState
            status={unavailable ? 503 : 500}
            locale={i18n.language}
            description={t("console.organization.unavailable")}
            action={<Button variant="outline" onClick={() => void refetch()}>{t("console.organization.retry")}</Button>}
          />
        ) : selection ? (
          // No tenant in this mode is a beginning: the invitation, or where
          // the application stands. /console/apply runs without a tenant.
          <MerchantSelectionProvider value={selection}>
            {onApply ? children : onConnections ? (
              <ConsoleScopeProvider value={selection.scope}>{children}</ConsoleScopeProvider>
            ) : !active ? <MerchantInvitation/> : (
              <ConsoleScopeProvider value={selection.scope}>{children}</ConsoleScopeProvider>
            )}
          </MerchantSelectionProvider>
        ) : (
          <ShellSkeleton/>
        )}
          {active && <BottomNavSpacer/>}
        </div>
        {active && <ConsoleBottomNav hasOrganization={active} reviewer={reviewer}/>}
      </main>
    </DensityScope>
  )
}

function ShellSkeleton() {
  return (
    <div className="space-y-4" aria-busy>
      <Skeleton className="h-6 w-40"/>
      <Skeleton className="h-4 w-full"/>
      <Skeleton className="h-4 w-5/6"/>
    </div>
  )
}

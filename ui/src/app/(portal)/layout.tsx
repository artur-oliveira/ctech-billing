"use client"

import {Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import Image from "next/image"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useEffect} from "react"
import {useTranslation} from "react-i18next"

import {LanguageSwitcher} from "@/components/LanguageSwitcher"
import {AccountMenu} from "@/components/AccountMenu"
import {NoBillingAccount} from "@/components/portal/NoBillingAccount"
import {PortalSpaceScope} from "@/components/portal/PortalSpaceScope"
import {PortalSpaceSwitch} from "@/components/portal/PortalSpaceSwitch"
import {TermsGate} from "@/components/portal/TermsGate"
import {isNoBillingAccount} from "@/lib/api/client"
import {getSession, portalKeys} from "@/lib/api/portal"
import {useAuth} from "@/lib/auth/AuthContext"
import {usePortalSpace} from "@/lib/portal/space"

const NAV = [
  {href: "/dashboard", labelKey: "portal.nav.home"},
  {href: "/invoices", labelKey: "portal.nav.invoices"},
  {href: "/subscriptions", labelKey: "portal.nav.subscriptions"},
] as const

/**
 * The consumer shell, and the gate in front of it.
 *
 * `data-density="comfortable"` is the whole of the portal/console difference
 * at the component level: every control from @aoctech/ui reads it and sizes
 * itself for a thumb. The console will set `compact` on its own shell and get
 * 32px controls from the identical components.
 *
 * The guard waits for `loading` before deciding. Skipping that check sends a
 * signed-in reader to /login on every hard refresh, because the boot-time
 * silent refresh has not answered yet when the first render happens. It is a
 * client-side guard and it is not the security boundary — the API rejects an
 * unauthenticated request whatever this renders. It exists so somebody whose
 * session ended sees a login instead of four failed panels.
 *
 * The tenant is never on this screen: the reader is a customer of CTech, and
 * the tenant is resolved from their session server-side. What can be on it is
 * whose bills these are — the person's own, or an organization they own or
 * administer (ADR 0025) — and only for someone who has such an organization.
 */
export default function PortalLayout({children}: LayoutProps<"/">) {
  const {t} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const {authenticated, loading} = useAuth()
  // Read so the shell re-renders on a change of selection: the session key
  // below carries the space (portalKeys), and so must this read of it.
  usePortalSpace()

  const {data: session, error: sessionError} = useQuery({
    queryKey: portalKeys.session,
    queryFn: getSession,
    enabled: authenticated,
  })

  // Signed in, nothing bought yet. Decided from the session alone rather than
  // per screen: every route below answers the same 403, so the alternative is
  // the same message three times under a nav whose every tab leads back to it.
  const noAccount = isNoBillingAccount(sessionError)

  useEffect(() => {
    if (!loading && !authenticated) router.replace("/login")
  }, [loading, authenticated, router])

  return (
    <div data-density="comfortable" className="min-h-dvh">
      <header className="border-b border-border">
        <div className="mx-auto flex h-16 max-w-2xl items-center justify-between gap-4 px-4">
          {/* The mark plus the wordmark, and the wordmark is the fourth place
              the brand colour appears — after the primary button, the active
              nav item and the selected row. A header that renders the company
              name in body ink is a header that could belong to anybody. */}
          <Link href="/dashboard" className="flex items-center gap-2.5 touch:min-h-11">
            <Image
              src="/android-chrome-192x192.png"
              alt=""
              width={28}
              height={28}
              priority
              className="size-7 rounded-lg"
            />
            <span className="text-base font-semibold tracking-[-0.02em] text-brand-600">
              CTech
              <span className="ml-1.5 font-normal text-muted-foreground">Billing</span>
            </span>
          </Link>

          {/* Also when there is no billing account: that reader has no name to
              show, and "Sair" is the only thing they can do here. A screen with
              no way out is how somebody signed into the wrong account gets
              stuck. */}
          {/* The person behind the avatar: the whole name and e-mail, the
              switch to the console, and Sair. It holds three things now, so it
              is a menu; it was plain text while Sair was its only entry. */}
          <div className="flex min-w-0 items-center gap-2">
            <LanguageSwitcher className="shrink-0"/>
            {(session || noAccount) && <AccountMenu view="portal"/>}
          </div>
        </div>
        {/* Whose bills: its own row, above the tabs it governs. Renders
            nothing for a person with no organization to pick. Stays with no
            billing account: an admin with nothing of their own still has the
            organization's bills to open. */}
        <div className="mx-auto max-w-2xl px-4">
          <PortalSpaceSwitch/>
        </div>
        {/* Hidden with no billing account. Three tabs that all lead to the same
            empty state read as three broken screens. */}
        <nav hidden={noAccount} className="mx-auto max-w-2xl px-4" aria-label={t("portal.nav.label")}>
          <ul className="-mb-px flex gap-1">
            {NAV.map(item => {
              const active =
                item.href === "/dashboard"
                  ? pathname === "/dashboard"
                  : pathname.startsWith(item.href)
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    aria-current={active ? "page" : undefined}
                    className={`inline-flex h-11 items-center border-b-2 px-3 text-sm transition-colors ${
                      active
                        ? "border-brand-600 font-medium text-brand-600"
                        : "border-transparent text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    {t(item.labelKey)}
                  </Link>
                </li>
              )
            })}
          </ul>
        </nav>
      </header>

      <main className="mx-auto max-w-2xl px-4 py-8 pb-20">
        {/* Four states, in this order. The empty account comes before the terms
            gate because there is no session to read `terms_accepted` from when
            it applies, and the gate comes before the children because it needs
            the session to have answered — rendering it on `undefined` would ask
            somebody who already agreed to agree again on every refresh. */}
        <PortalSpaceScope>
          {loading || !authenticated ? (
            <GateSkeleton/>
          ) : noAccount ? (
            <NoBillingAccount/>
          ) : session && !session.terms_accepted ? (
            <TermsGate/>
          ) : (
            children
          )}
        </PortalSpaceScope>
      </main>
    </div>
  )
}

/** Held while the silent refresh answers. Not a spinner and not the word
 *  "carregando": the shell is already on screen, and what comes next is
 *  content in this shape. */
function GateSkeleton() {
  return (
    <div className="space-y-6" aria-busy>
      <Skeleton className="h-4 w-52"/>
      <Skeleton className="h-9 w-48"/>
      <Skeleton className="h-4 w-64"/>
    </div>
  )
}

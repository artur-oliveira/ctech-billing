"use client"

import {Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import Image from "next/image"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useEffect} from "react"

import {ModeSwitch} from "@/components/console/ModeSwitch"
import {NoOrganization} from "@/components/console/NoOrganization"
import {SpaceSwitch} from "@/components/finance/SpaceSwitch"
import {statusOf} from "@/lib/api/client"
import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {useAuth} from "@/lib/auth/AuthContext"
import {useMode} from "@/lib/console/useMode"

/** Invoicing: an operator's sections, shown only with an organization. */
const INVOICING_NAV = [
  {href: "/console/overview", label: "Visão geral"},
  {href: "/console/invoices", label: "Faturas"},
  {href: "/console/subscriptions", label: "Assinaturas"},
  {href: "/console/customers", label: "Clientes"},
  {href: "/console/catalog", label: "Catálogo"},
  {href: "/console/settings", label: "Configurações"},
] as const

/** Finance: everyone's, in a personal space or an organization's (ADR 0025). */
const FINANCE_HREF = "/console/finance"

/**
 * The operator shell — the second of the two the app ships, and the same
 * components as the first at a different density.
 *
 * `data-density="compact"` is the whole of that difference: every control from
 * `@aoctech/ui` reads it and sizes itself to 32px instead of 44. An operator
 * works in this all day and wants rows on screen; the same person opens the
 * portal twice a month on a phone and wants a thumb target. Neither is a prop
 * a call site remembers to pass.
 *
 * Wider than the portal, too: `max-w-6xl` against `max-w-2xl`. The portal reads
 * one bill; this reads a table.
 *
 * The mode is in the header and never anywhere else. It is the one thing an
 * operator switches, acting on the wrong one is the expensive mistake, and the
 * organization is *not* switchable — it comes from the signed-in owner, and a
 * tenant picker here would be asking a question the server already answered
 * (ADR 0011).
 */
export default function ConsoleLayout({children}: LayoutProps<"/console">) {
  const pathname = usePathname()
  const router = useRouter()
  const {authenticated, loading, logout} = useAuth()
  const mode = useMode()

  const {data: session, error} = useQuery({
    queryKey: consoleKeys.session(mode),
    queryFn: () => getConsoleSession(mode),
    enabled: authenticated,
    retry: false,
  })

  // Signed in, no organization: the invoicing sections are not theirs, but
  // Finanças is — everyone has a personal space. Decided from the session alone
  // rather than per screen.
  const noOrganization = statusOf(error) === 403 || statusOf(error) === 404
  const inFinance = pathname === FINANCE_HREF || pathname.startsWith(FINANCE_HREF + "/")

  useEffect(() => {
    if (!loading && !authenticated) router.replace("/login")
  }, [loading, authenticated, router])

  // The console's default landing is invoicing's overview; a person with no
  // organization lands on their finances instead of on an explanation.
  useEffect(() => {
    if (noOrganization && pathname === "/console/overview") router.replace(FINANCE_HREF)
  }, [noOrganization, pathname, router])

  const nav = [...(noOrganization ? [] : INVOICING_NAV), {href: FINANCE_HREF, label: "Finanças"}]

  return (
    <div data-density="compact" className="min-h-dvh">
      <header className="border-b border-border">
        <div className="mx-auto flex min-h-14 max-w-6xl flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-2">
          <div className="flex min-w-0 items-center gap-4">
            <Link href="/console/overview" className="flex shrink-0 items-center gap-2.5">
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
            {/* In Finanças the space switcher names the space; the invoicing
                organization's name here would read as the space and contradict
                it ("CTech Tecnologia" beside "Pessoal"). */}
            {session && !inFinance && (
              <span className="truncate border-l border-border pl-4 text-sm text-foreground">
                {session.display_name}
              </span>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-3">
            {/* Space and mode side by side, so "Pessoal · Teste" reads as one
                answer. The space only exists in Finanças: invoicing's
                organization comes from the signed-in owner (ADR 0011). */}
            {inFinance && <SpaceSwitch/>}
            <ModeSwitch/>
            {/* The way back to the other shell, always. The same person holds
                both, and making them retype a URL to look at their own bill is
                the "which one are you" question this product does not ask. */}
            <Link
              href="/dashboard"
              className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              Minhas cobranças
            </Link>
            <button
              type="button"
              onClick={logout}
              className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              Sair
            </button>
          </div>
        </div>

        <nav className="mx-auto max-w-6xl px-4" aria-label="Seções">
          <ul className="-mb-px flex gap-1 overflow-x-auto">
            {nav.map(item => {
              const active = pathname.startsWith(item.href)
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    aria-current={active ? "page" : undefined}
                    className={`inline-flex h-10 items-center whitespace-nowrap border-b-2 px-3 text-sm transition-colors ${
                      active
                        ? "border-brand-600 font-medium text-brand-600"
                        : "border-transparent text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    {item.label}
                  </Link>
                </li>
              )
            })}
          </ul>
        </nav>
      </header>

      {/* The test-mode band. Deliberately the loudest thing on the page after
          the danger colour: every number below it is fake, and an operator who
          forgets that voids a real invoice believing it is a sandbox one. */}
      {mode === "test" && (
        <p className="bg-warning/12 border-b border-warning/30 px-4 py-2 text-center text-xs text-foreground">
          Modo de teste; nada aqui cobra dinheiro de verdade.
        </p>
      )}

      <main className="mx-auto max-w-6xl px-4 py-8 pb-20">
        {loading || !authenticated ? (
          <ShellSkeleton/>
        ) : noOrganization && !inFinance ? (
          <NoOrganization/>
        ) : (
          children
        )}
      </main>
    </div>
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

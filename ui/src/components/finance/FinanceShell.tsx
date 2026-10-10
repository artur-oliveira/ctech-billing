"use client"

import {BottomNavSpacer, DensityScope, Skeleton} from "@aoctech/ui"
import Image from "next/image"
import Link from "next/link"
import {useRouter} from "next/navigation"
import {type ReactNode, useEffect} from "react"
import {useTranslation} from "react-i18next"

import {AccountMenu} from "@/components/AccountMenu"
import {LanguageSwitcher} from "@/components/LanguageSwitcher"
import {ModeSwitch} from "@/components/console/ModeSwitch"
import {FinanceBottomNav} from "@/components/finance/FinanceBottomNav"
import {FinanceNav} from "@/components/finance/FinanceNav"
import {SpaceSwitch} from "@/components/finance/SpaceSwitch"
import {useAuth} from "@/lib/auth/AuthContext"
import {useMode} from "@/lib/console/useMode"
import {FINANCE_HREF} from "@/lib/finance/href"

/**
 * Finanças, the third area beside the portal and the console. It was a section
 * of the console; it is its own product now, because most people who use it
 * have no organization and nothing else in the console is theirs.
 *
 * The console's density and width (a person keeps their books in tables) and
 * its header grammar: the mark, then space and mode side by side so
 * "Pessoal · Teste" reads as one answer, then the language and the avatar,
 * where the three areas meet. The sections are Finanças' own: a column on a
 * laptop, a picker on a tablet, a bar at the bottom on a phone.
 *
 * No session probe: everybody signed in has a personal space (ADR 0025), so
 * there is no "not yours" state to decide before rendering.
 */
export function FinanceShell({children}: {children: ReactNode}) {
  const {t} = useTranslation()
  const router = useRouter()
  const {authenticated, loading} = useAuth()
  const mode = useMode()

  useEffect(() => {
    if (!loading && !authenticated) router.replace("/login")
  }, [loading, authenticated, router])

  return (
    <DensityScope density="compact" className="min-h-dvh">
      <header className="border-b border-border">
        <div className="mx-auto flex min-h-14 max-w-6xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2">
          <Link href={FINANCE_HREF} className="order-1 flex shrink-0 items-center gap-2.5 touch:min-h-11">
            <Image
              src="/android-chrome-192x192.png"
              alt=""
              width={24}
              height={24}
              className="size-6 rounded-md"
            />
            <span className="text-sm font-semibold tracking-[-0.02em] text-brand-600">
              CTech
              <span className="ml-1.5 font-normal text-muted-foreground">{t("finance.shell.product")}</span>
            </span>
          </Link>

          <div className="order-3 flex w-full min-w-0 items-center gap-x-3 gap-y-2 sm:order-2 sm:ml-auto sm:w-auto">
            <SpaceSwitch/>
            <ModeSwitch/>
          </div>
          <div className="order-2 ml-auto flex items-center gap-2 sm:order-3 sm:ml-0">
            <LanguageSwitcher/>
            <AccountMenu view="finance"/>
          </div>
        </div>
      </header>

      {/* The same band as the console's: every number below it is a test one. */}
      {mode === "test" && (
        <p className="bg-warning/12 border-b border-warning/30 px-4 py-2 text-center text-xs text-foreground">
          {t("finance.shell.testBanner")}
        </p>
      )}

      <main className="mx-auto max-w-6xl px-4 py-8 pb-20">
        {loading || !authenticated ? (
          <div className="space-y-4" aria-busy>
            <Skeleton className="h-6 w-40"/>
            <Skeleton className="h-4 w-full"/>
            <Skeleton className="h-4 w-5/6"/>
          </div>
        ) : (
          <div className="grid gap-6 lg:grid-cols-[11rem_minmax(0,1fr)] lg:gap-10">
            <aside className="max-md:hidden lg:sticky lg:top-6 lg:self-start">
              <FinanceNav/>
            </aside>
            <div className="min-w-0">
              {children}
              <BottomNavSpacer/>
            </div>
            <FinanceBottomNav/>
          </div>
        )}
      </main>
    </DensityScope>
  )
}

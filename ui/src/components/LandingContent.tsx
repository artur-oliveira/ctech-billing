"use client"

import Image from "next/image"
import {useTranslation} from "react-i18next"

import {LandingCta} from "@/components/LandingCta"
import {LanguageSwitcher} from "@/components/LanguageSwitcher"

const ACCOUNTS_LEGAL_URL = "https://accounts.aoctech.app/legal"
const PRIVACY_URL = "https://accounts.aoctech.app/privacy"
const TERMS_URL = "https://accounts.aoctech.app/terms"

/**
 * The public front door. A client component so it can translate; the page
 * keeps the static metadata. One action: sign in.
 */
export function LandingContent() {
  const {t} = useTranslation()
  return (
    <div className="flex min-h-dvh flex-col">
      <div className="flex justify-end px-4 pt-3">
        <LanguageSwitcher/>
      </div>
      <main className="flex flex-1 items-center px-6 py-10">
        <section className="mx-auto w-full max-w-md">
          <div className="flex items-center gap-2.5">
            <Image
              src="/android-chrome-192x192.png"
              alt=""
              width={32}
              height={32}
              priority
              className="size-8 rounded-lg"
            />
            <span className="text-base font-semibold tracking-[-0.02em] text-brand-600">
              CTech
              <span className="ml-1.5 font-normal text-muted-foreground">Billing</span>
            </span>
          </div>

          <h1 className="mt-10 text-balance text-3xl font-semibold tracking-[-0.02em] text-foreground">
            {t("landing.tagline")}
          </h1>

          <div className="mt-8">
            <LandingCta/>
          </div>

          <p className="mt-4 text-pretty text-sm text-muted-foreground">{t("landing.payLink")}</p>
        </section>
      </main>

      <footer
        className="mx-auto flex w-full max-w-3xl flex-col gap-3 px-6 py-8 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
        <p>© {new Date().getFullYear()} A O CARVALHO TECH</p>
        {/* Plain anchors: these leave the app for ctech-account, and next/link
            would prefetch a cross-origin document it cannot use. */}
        <div className="flex flex-wrap gap-x-5 gap-y-2">
          <a href={TERMS_URL} className="hover:text-foreground">{t("landing.footer.terms")}</a>
          <a href={PRIVACY_URL} className="hover:text-foreground">{t("landing.footer.privacy")}</a>
          <a href={ACCOUNTS_LEGAL_URL} className="hover:text-foreground">{t("landing.footer.legal")}</a>
        </div>
      </footer>
    </div>
  )
}

import type {Metadata} from "next"
import Image from "next/image"

import {LandingCta} from "@/components/LandingCta"

const ACCOUNTS_LEGAL_URL = "https://accounts.aoctech.app/legal"
const PRIVACY_URL = "https://accounts.aoctech.app/privacy"
const TERMS_URL = "https://accounts.aoctech.app/terms"

/**
 * The one indexable route in this app, overriding the root layout's `noindex`.
 *
 * It is safe to index precisely because it carries no data: a name, a sentence
 * and a login button. Every route below it is one customer's billing history
 * and stays out of every crawler's reach.
 */
export const metadata: Metadata = {
  title: {absolute: "CTech Billing — suas faturas e assinaturas"},
  description:
    "Portal de cobranças da CTech. Veja faturas em aberto, pague com PIX em segundos e acompanhe suas assinaturas.",
  robots: {index: true, follow: true},
  alternates: {canonical: "/"},
}

/**
 * The public front door.
 *
 * It says what this is and offers one action, because there is exactly one
 * thing to do: sign in and look at your bills. No feature grid, no pricing
 * table, no testimonial — this is not a product being sold to the reader, it is
 * the place they were sent to pay for one they already have.
 */
export default function Landing() {
  return (
    <div className="flex min-h-dvh flex-col">
      <main className="flex flex-1 items-center px-6 py-16">
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
            Suas faturas, num lugar só
          </h1>
          <p className="mt-4 text-pretty text-base leading-relaxed text-muted-foreground">
            Veja o que está em aberto, pague com PIX em segundos e acompanhe suas assinaturas da
            CTech. Sem boleto, sem ligação, sem esperar compensar.
          </p>

          <div className="mt-8">
            <LandingCta />
          </div>

          <p className="mt-4 text-sm text-muted-foreground">
            Recebeu um link de pagamento por e-mail? Ele abre a fatura direto, sem entrar.
          </p>
        </section>
      </main>

      <footer className="mx-auto flex w-full max-w-3xl flex-col gap-3 px-6 py-8 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
        <p>© {new Date().getFullYear()} A O CARVALHO TECH</p>
        {/* Plain anchors: these leave the app for ctech-account, and next/link
            would prefetch a cross-origin document it cannot use. */}
        <div className="flex flex-wrap gap-x-5 gap-y-2">
          <a href={TERMS_URL} className="hover:text-foreground">
            Termos
          </a>
          <a href={PRIVACY_URL} className="hover:text-foreground">
            Privacidade
          </a>
          <a href={ACCOUNTS_LEGAL_URL} className="hover:text-foreground">
            Central legal
          </a>
        </div>
      </footer>
    </div>
  )
}

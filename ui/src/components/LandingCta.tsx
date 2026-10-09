"use client"

import {Button} from "@aoctech/ui"
import {useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

import {useAuth} from "@/lib/auth/AuthContext"

/**
 * The one interactive thing on the landing page.
 *
 * Split out so the page itself can stay a server component and export its own
 * `metadata` — it is the only route in this app that wants to be indexed, and
 * the root layout's `noindex` has to be overridden somewhere that can.
 *
 * The label changes rather than duplicating: somebody already signed in is
 * offered their invoices, not a second login that would bounce them straight
 * back here.
 */
export function LandingCta() {
  const {authenticated, loading, login} = useAuth()
  const router = useRouter()
  const {t} = useTranslation()

  return (
    <Button
      block
      className="sm:w-auto"
      disabled={loading}
      onClick={() => (authenticated ? router.push("/dashboard") : login("/dashboard"))}
    >
      {loading ? t("common.loading") : authenticated ? t("landing.openApp") : t("landing.signIn")}
    </Button>
  )
}

"use client"

import {Button} from "@aoctech/ui"
import {useEffect} from "react"
import {useTranslation} from "react-i18next"

import {StatusScreen} from "@/components/StatusScreen"
import {useAuth} from "@/lib/auth/AuthContext"

/**
 * Where an expired session lands.
 *
 * It is not a form. ctech-account owns every credential in the family and this
 * app never sees one; the whole of "logging in" here is a redirect to the
 * authorization endpoint. A page that looked like a login form would be asking
 * for a password on a domain that must never receive one — which is the shape
 * of a phishing page, taught to customers by us.
 *
 * Somebody who arrives already signed in is sent on rather than shown a button
 * that would bounce them straight back.
 */
export default function LoginPage() {
  const {authenticated, loading, login} = useAuth()
  const {t} = useTranslation()

  useEffect(() => {
    if (!loading && authenticated) window.location.replace("/dashboard")
  }, [loading, authenticated])

  return (
    <StatusScreen
      title={t("auth.login.title")}
      description={t("auth.login.description")}
      action={
        <Button onClick={() => login("/dashboard")} disabled={loading || authenticated}>
          {loading ? t("auth.login.checking") : t("auth.login.signIn")}
        </Button>
      }
    />
  )
}

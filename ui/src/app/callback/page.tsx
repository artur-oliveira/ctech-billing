"use client"

import {Button} from "@aoctech/ui"
import Link from "next/link"
import {useRouter, useSearchParams} from "next/navigation"
import {Suspense, useEffect, useRef, useState} from "react"

import {StatusScreen} from "@/components/StatusScreen"
import {useAuth} from "@/lib/auth/AuthContext"
import {exchangeCode} from "@/lib/auth/oauth"

/**
 * Where ctech-account sends the browser back to, carrying `code` and `state`.
 *
 * Nothing here is a screen anybody should read: the exchange takes one round
 * trip and then this page is gone. What it does need is a failure state, and a
 * useful one — an OAuth callback that fails silently leaves somebody staring at
 * a spinner with no idea whether they are signed in.
 */
export default function CallbackPage() {
  return (
    <Suspense fallback={<Working />}>
      <Callback />
    </Suspense>
  )
}

/** The subset of RFC 6749 §4.1.2.1 errors worth a different sentence. Anything
 *  else is "try again", because anything else is ours to fix, not theirs. */
function messageFor(error: string): string {
  switch (error) {
    case "access_denied":
      return "Você cancelou a entrada. Nada foi alterado — pode tentar de novo quando quiser."
    case "login_required":
    case "interaction_required":
      return "Sua sessão expirou antes de terminar. Entre de novo."
    default:
      return "Não conseguimos concluir a entrada. Tente de novo — se continuar, fale com a gente."
  }
}

function Callback() {
  const params = useSearchParams()
  const router = useRouter()
  const {onCallback} = useAuth()
  // Only the exchange's failure is state. What the URL already says is derived
  // during render — deciding it in an effect would paint one frame of
  // "Entrando…" over a request that was never going to be made.
  const [exchangeFailed, setExchangeFailed] = useState(false)
  // React mounts effects twice in development. The authorization code is
  // single-use, so a second run would exchange an already-spent code and report
  // a failure on a login that worked.
  const ran = useRef(false)

  const code = params.get("code")
  const state = params.get("state")
  const denied = params.get("error")
  const usable = !denied && Boolean(code) && Boolean(state)

  useEffect(() => {
    if (!usable || ran.current) return
    ran.current = true

    void (async () => {
      try {
        const {accessToken, idToken, returnTo} = await exchangeCode(code!, state!)
        onCallback(accessToken, idToken)
        router.replace(returnTo || "/dashboard")
      } catch {
        setExchangeFailed(true)
      }
    })()
  }, [usable, code, state, onCallback, router])

  const error = denied
    ? messageFor(denied)
    : !usable
      ? messageFor("invalid_request")
      : exchangeFailed
        ? messageFor("server_error")
        : null

  if (error) {
    return (
      <StatusScreen
        title="Não deu para entrar"
        description={error}
        action={
          <Button variant="outline" render={<Link href="/login" />}>
            Tentar de novo
          </Button>
        }
      />
    )
  }

  return <Working />
}

function Working() {
  return (
    <StatusScreen
      title="Entrando…"
      description="Confirmando sua identidade com a conta CTech. Isso leva um instante."
    />
  )
}

"use client"

import {createContext, useCallback, useContext, useEffect, useState} from "react"
import type {ReactNode} from "react"

import {registerRefresh, setAccessToken} from "@/lib/api/client"
import {MOCK_CUSTOMER, USE_MOCK} from "@/lib/mockConfig"

import {decodeIdToken, doRefresh, logout as endSession, startOAuthFlow} from "./oauth"

interface Auth {
  /** The signed-in person's display name, or null. Never authorize on this — it
   *  comes from an unverified id_token decode and exists to greet somebody. */
  name: string | null
  authenticated: boolean
  /** True until the boot-time silent refresh has answered. Every guard waits on
   *  it; sending somebody to /login while it is true would bounce a signed-in
   *  reader out on every hard refresh. */
  loading: boolean
  login: (returnTo?: string) => void
  logout: () => void
  onCallback: (accessToken: string, idToken: string | null) => void
}

const AuthContext = createContext<Auth | undefined>(undefined)

const NAME_KEY = "ctech-billing-name"

export function useAuth(): Auth {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth precisa de AuthProvider acima")
  return ctx
}

export function AuthProvider({children}: {children: ReactNode}) {
  // Seeded during the first render rather than corrected by an effect. The
  // cached name is what stops the header flashing empty on every load, and
  // writing it from an effect would paint the empty frame first — which is the
  // flash it exists to prevent. It is a label, never a permission.
  const [name, setName] = useState<string | null>(() => {
    if (USE_MOCK) return MOCK_CUSTOMER.name
    if (typeof window === "undefined") return null
    return window.localStorage.getItem(NAME_KEY)
  })
  const [authenticated, setAuthenticated] = useState(USE_MOCK)
  const [loading, setLoading] = useState(!USE_MOCK)

  const adopt = useCallback((accessToken: string) => {
    setAccessToken(accessToken)
    setAuthenticated(true)
  }, [])

  const refresh = useCallback(async (): Promise<string | null> => {
    if (USE_MOCK) return "mock"
    const token = await doRefresh()
    if (!token) {
      setAccessToken(null)
      setAuthenticated(false)
      return null
    }
    adopt(token)
    return token
  }, [adopt])

  // The axios client owns the 401 retry, but the token it needs is this
  // provider's to fetch. Registering the function rather than importing the
  // provider keeps the dependency pointing one way.
  useEffect(() => {
    registerRefresh(refresh)
  }, [refresh])

  // The boot-time silent refresh. It answers the one question every guard is
  // waiting on: is there a session to resume? Until it does, `loading` is true
  // and nothing redirects anybody anywhere.
  useEffect(() => {
    if (USE_MOCK) {
      setAccessToken("mock")
      return
    }
    let cancelled = false
    void (async () => {
      await refresh()
      if (!cancelled) setLoading(false)
    })()
    return () => {
      cancelled = true
    }
  }, [refresh])

  const login = useCallback((returnTo = "/dashboard") => {
    if (USE_MOCK) {
      setAuthenticated(true)
      return
    }
    void startOAuthFlow(returnTo)
  }, [])

  const logout = useCallback(() => {
    window.localStorage.removeItem(NAME_KEY)
    setAccessToken(null)
    setAuthenticated(false)
    setName(null)
    if (USE_MOCK) return
    // Awaited before the redirect: revoke has to land, or the refresh chain
    // outlives the logout.
    void endSession()
  }, [])

  const onCallback = useCallback(
    (accessToken: string, idToken: string | null) => {
      adopt(accessToken)
      // first_name alone, not the full name: the header is a greeting, and
      // `username` is a login handle rather than something to greet somebody by.
      const claims = idToken ? decodeIdToken(idToken) : null
      const display = claims?.first_name ?? claims?.username ?? null
      if (display) {
        setName(display)
        window.localStorage.setItem(NAME_KEY, display)
      }
    },
    [adopt]
  )

  return (
    <AuthContext.Provider
      value={{name, authenticated, loading, login, logout, onCallback}}
    >
      {children}
    </AuthContext.Provider>
  )
}

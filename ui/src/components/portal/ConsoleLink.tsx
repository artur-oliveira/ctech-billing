"use client"

import {useQuery} from "@tanstack/react-query"
import Link from "next/link"

import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {useAuth} from "@/lib/auth/AuthContext"

/**
 * The portal's way into the console.
 *
 * It renders **only for somebody who actually has an organization**, and that
 * is the whole reason it is a component with a query rather than a link in the
 * shell's markup. Everybody who signs in is a customer; only some are also
 * operators, and provisioning is manual (assessment D4). A permanent "Console"
 * link would send the majority to a screen explaining that the product they
 * clicked is not theirs — which is the "which one are you?" question this app
 * is built not to ask.
 *
 * The probe is the console's own session route, so the answer is the server's:
 * a 403 or 404 means no organization and the link stays absent. It is asked
 * once per tab and shares the console's own query key, so opening the console
 * afterwards renders from a warm cache.
 *
 * A failure is silence, not an error. This is a shortcut; nobody's bill depends
 * on it.
 */
export function ConsoleLink() {
  const {authenticated} = useAuth()

  const {data} = useQuery({
    // Live: the portal has no mode switch, and the console opens on live too.
    queryKey: consoleKeys.session("live"),
    queryFn: () => getConsoleSession("live"),
    enabled: authenticated,
    retry: false,
    // An operator does not gain or lose an organization mid-session, so this is
    // asked once and left alone.
    staleTime: Infinity,
  })

  if (!data) return null

  return (
    <Link
      href="/console/overview"
      className="shrink-0 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      Console
    </Link>
  )
}

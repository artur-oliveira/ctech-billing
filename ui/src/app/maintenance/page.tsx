"use client"

import {Button} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {useRouter, useSearchParams} from "next/navigation"
import {Suspense, useEffect} from "react"

import {StatusScreen} from "@/components/StatusScreen"
import {getHealth} from "@/lib/api/portal"
import {safeReturn} from "@/lib/returnTo"

/**
 * Where a 503 sends the reader. The API is saying it is deliberately down, so
 * there is nothing per-screen to retry and every screen would otherwise draw
 * its own retry button for a thing no retry can fix.
 *
 * It probes rather than waits. A 503 is also what a rolling deploy answers for
 * a few seconds, and somebody bounced here by a blip should be carried back
 * without having to work out that they can. The manual button exists for the
 * reader who does not want to wait fifteen seconds to find out.
 */
export default function Maintenance() {
  return (
    <Suspense fallback={<Screen/>}>
      <MaintenanceScreen/>
    </Suspense>
  )
}

function MaintenanceScreen() {
  const router = useRouter()
  // `?from` is attacker-controlled — a link to /maintenance?from=… is a link
  // anybody can send. safeReturn is what keeps it same-site; see its test.
  const back = safeReturn(useSearchParams().get("from"), "/dashboard")

  const probe = useQuery({
    queryKey: ["maintenance-probe"],
    // Health, not a portal route: whoever is here may also have an expired
    // session, and a 401 would be turned into a redirect to /login, which
    // cannot load either while the API is down.
    queryFn: getHealth,
    retry: false,
    refetchInterval: 15_000,
    refetchOnWindowFocus: true,
    gcTime: 0,
  })

  useEffect(() => {
    if (probe.isSuccess) router.replace(back)
  }, [probe.isSuccess, router, back])

  return (
    <Screen
      action={
        <Button variant="outline" onClick={() => void probe.refetch()} disabled={probe.isFetching}>
          {probe.isFetching ? "Verificando…" : "Verificar agora"}
        </Button>
      }
    />
  )
}

function Screen({action}: { action?: React.ReactNode }) {
  return (
    <StatusScreen
      title="Estamos em manutenção"
      description="O sistema de cobranças está fora do ar por pouco tempo. Nenhuma fatura vence enquanto isso e nada foi cobrado. Esta página volta sozinha assim que terminarmos."
      action={action}
    />
  )
}

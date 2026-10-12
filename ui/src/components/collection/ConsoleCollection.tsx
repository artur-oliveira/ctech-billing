"use client"

import {Skeleton} from "@aoctech/ui"
import {useQuery, useQueryClient} from "@tanstack/react-query"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {messageFor} from "@/lib/api/client"
import {consoleKeys, getConsoleSettings, setCollection} from "@/lib/api/console"
import type {ConsoleScope} from "@/lib/console/merchant"

import {CollectionSettings} from "./CollectionSettings"

/**
 * The console's "Cobrança via Inter" on the connection screen (spec § 11.2):
 * shown to a merchant tenant only (an organization with no tenant has no
 * invoices to collect, and the settings read answers 404 for it).
 */
export function ConsoleCollection({scope, provider}: {scope: ConsoleScope; provider: string}) {
  const {t} = useTranslation()
  const qc = useQueryClient()
  const q = useQuery({queryKey: consoleKeys.settings(scope), queryFn: () => getConsoleSettings(scope), retry: false})
  if (q.isPending) return <Skeleton className="h-24 w-full max-w-xl"/>
  if (q.isError) return null
  const active = q.data.organization.payout_rail === "byok" && q.data.organization.payout_provider === provider
  return (
    <section aria-labelledby="conn-collection" className="space-y-3">
      <h2 id="conn-collection" className="text-base font-semibold text-foreground">{t("connections.collection.title")}</h2>
      <CollectionSettings initial={q.data.collection ?? {fine_bp: 0, monthly_interest_bp: 0}} active={active}
        onSubmit={async v => {
          try {
            const next = await setCollection(scope, {provider, ...v})
            qc.setQueryData(consoleKeys.settings(scope), next)
            toast.success(t("connections.collection.saved"))
          } catch (e) {
            toast.error(messageFor(e))
          }
        }}/>
    </section>
  )
}

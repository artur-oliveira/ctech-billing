"use client"

import {Alert, Button, EmptyState, PageHeader, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ChevronLeft, FlaskConical} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {connect, connectionKeys, listConnections} from "@/lib/api/connections"
import {messageFor, problemCode} from "@/lib/api/client"
import type {ConnectionsScope} from "@/lib/api/connectionsTypes"
import {codeMessage} from "@/lib/errors"
import {useIntent} from "@/lib/api/idempotency"

import {ConnectionHealth} from "./ConnectionHealth"
import {InterOnboarding} from "./InterOnboarding"

/**
 * The one Inter screen, for both areas (BYOK spec § 11): the console's
 * /console/connections/inter and Finanças' /finance/settings/connections/inter
 * render it with their own scope and back link. Live: the guided onboarding
 * until connected, then the connection's health. Test: CTech's own
 * integration against Inter's sandbox, turned on with one press.
 */
export function InterConnectionScreen({scope, backHref, statementHref}: {scope: ConnectionsScope; backHref: string; statementHref?: (accountId: string) => string}) {
  const {t} = useTranslation()
  const q = useQuery({queryKey: connectionKeys.list(scope), queryFn: () => listConnections(scope)})
  const conn = q.data?.data.find(c => c.provider === "inter") ?? null
  const connected = conn !== null && conn.state !== "onboarding"

  return (
    <div className="space-y-8">
      <PageHeader
        lead={<Link href={backHref} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground touch:min-h-11"><ChevronLeft aria-hidden className="size-4"/>{t("connections.screen.back")}</Link>}
        title={t("connections.providers.inter")}
        description={connected ? undefined : t("connections.intro")}
      />
      {q.isPending ? (
        <div className="space-y-3" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/6"/></div>
      ) : q.isError ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : connected ? (
        <ConnectionHealth scope={scope} connection={conn} statementHref={statementHref && conn.ledger_account_id ? statementHref(conn.ledger_account_id) : undefined}/>
      ) : scope.mode === "test" ? (
        <SandboxBlock scope={scope}/>
      ) : (
        <InterOnboarding scope={scope} connection={conn} providers={q.data?.providers ?? []}/>
      )}
    </div>
  )
}

function SandboxBlock({scope}: {scope: ConnectionsScope}) {
  const {t} = useTranslation()
  const qc = useQueryClient()
  const intent = useIntent()
  const enable = useMutation({
    mutationFn: () => connect(scope, "inter", null, intent.key()),
    onSuccess: c => {
      intent.done()
      qc.setQueryData(connectionKeys.one(scope, "inter"), c)
      void qc.invalidateQueries({queryKey: connectionKeys.list(scope)})
      void qc.invalidateQueries({queryKey: ["finance"]})
      toast.success(t("connections.sandbox.enabled"))
    },
  })
  const code = problemCode(enable.error)
  return (
    <div className="max-w-xl space-y-4">
      <EmptyState
        icon={<FlaskConical aria-hidden/>}
        title={t("connections.sandbox.title")}
        description={t("connections.sandbox.body")}
        action={<Button variant="brand" disabled={enable.isPending} onClick={() => enable.mutate()}>{t("connections.sandbox.enable")}</Button>}
      />
      {enable.isError && (
        <Alert tone="danger" title={t("connections.sandbox.title")}>{code?.startsWith("byok_") ? codeMessage(code) : messageFor(enable.error)}</Alert>
      )}
    </div>
  )
}

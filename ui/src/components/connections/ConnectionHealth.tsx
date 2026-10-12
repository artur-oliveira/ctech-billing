"use client"

import {Alert, Badge, Button, buttonVariants, cn} from "@aoctech/ui"
import {useMutation, useQueryClient} from "@tanstack/react-query"
import {AlertTriangle, CheckCircle2, Circle, Clock} from "lucide-react"
import Link from "next/link"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {connectionKeys, disconnect, renewCertificate, syncNow, testConnection} from "@/lib/api/connections"
import {messageFor, problemCode} from "@/lib/api/client"
import type {Connection, ConnectionStatus, ConnectionsScope} from "@/lib/api/connectionsTypes"
import {codeMessage} from "@/lib/errors"
import {useIntent} from "@/lib/api/idempotency"
import {instantShortDate} from "@/lib/format"

import {DisconnectDialog} from "./DisconnectDialog"
import {RenewDialog} from "./RenewDialog"

const TONE: Record<ConnectionStatus, "neutral" | "positive" | "attention" | "urgent"> = {
  onboarding: "neutral", active: "positive", degraded: "attention", expired: "urgent", disconnected: "neutral",
}
const GLYPH = {neutral: Circle, positive: CheckCircle2, attention: Clock, urgent: AlertTriangle} as const

/** A status as a word and a glyph, never colour alone. */
export function ConnectionStatusBadge({status}: {status: ConnectionStatus}) {
  const {t} = useTranslation()
  const tone = TONE[status]
  const Glyph = GLYPH[tone]
  return <Badge tone={tone}><Glyph aria-hidden/>{t(`connections.status.${status}`)}</Badge>
}

/** "14:05" or "10/10/2026 14:05", in São Paulo. */
function when(instant: string, locale: string): string {
  const d = new Date(instant)
  const sameDay = new Date().toDateString() === d.toDateString()
  return new Intl.DateTimeFormat(locale, sameDay
    ? {hour: "2-digit", minute: "2-digit", timeZone: "America/Sao_Paulo"}
    : {day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit", timeZone: "America/Sao_Paulo"}).format(d)
}

/**
 * A connected bank (BYOK spec § 11.2): its state in words, what it does, the
 * certificate's expiry from D-90, the last sync and the last error in words,
 * and the actions. Blocks are ruled, not boxed; one warning at a time.
 */
export function ConnectionHealth({scope, connection: c, statementHref}: {scope: ConnectionsScope; connection: Connection; statementHref?: string}) {
  const {t, i18n} = useTranslation()
  const qc = useQueryClient()
  const [renewing, setRenewing] = useState(false)
  const [leaving, setLeaving] = useState(false)
  const testIntent = useIntent(), syncIntent = useIntent(), renewIntent = useIntent(), leaveIntent = useIntent()
  const refresh = (next?: Connection) => {
    if (next) qc.setQueryData(connectionKeys.one(scope, "inter"), next)
    void qc.invalidateQueries({queryKey: connectionKeys.one(scope, "inter")})
    void qc.invalidateQueries({queryKey: connectionKeys.list(scope)})
  }
  const words = (e: unknown) => {
    const code = problemCode(e)
    return code?.startsWith("byok_") ? codeMessage(code) : messageFor(e)
  }

  const test = useMutation({
    mutationFn: () => testConnection(scope, "inter", testIntent.key()),
    onSuccess: next => { testIntent.done(); refresh(next); toast.success(t("connections.screen.testOk")) },
    onError: e => { testIntent.done(); refresh(); toast.error(words(e)) },
  })
  const sync = useMutation({
    mutationFn: () => syncNow(scope, "inter", syncIntent.key()),
    onSuccess: r => {
      syncIntent.done()
      refresh()
      void qc.invalidateQueries({queryKey: ["finance"]})
      toast.success(t("connections.screen.syncStarted", {count: r.posted}))
    },
    onError: e => { syncIntent.done(); toast.error(words(e)) },
  })
  const renew = useMutation({
    mutationFn: ({cert, key}: {cert: File; key: File}) => renewCertificate(scope, "inter", cert, key, renewIntent.key()),
    onSuccess: next => { renewIntent.done(); refresh(next); setRenewing(false); toast.success(t("connections.screen.renewed")) },
  })
  const leave = useMutation({
    mutationFn: () => disconnect(scope, "inter", leaveIntent.key()),
    onSuccess: () => {
      leaveIntent.done()
      setLeaving(false)
      qc.setQueryData(connectionKeys.one(scope, "inter"), null)
      refresh()
      void qc.invalidateQueries({queryKey: ["finance"]})
      toast.success(t("connections.disconnect.done"))
    },
    onError: e => toast.error(words(e)),
  })

  const merchant = c.kind !== "sandbox_shared"
  const days = c.cert_days_left
  const expiry = c.cert_not_after ? instantShortDate(c.cert_not_after) : undefined
  const banner = c.status === "expired" ? {tone: "danger" as const, title: t("connections.screen.expiredTitle"), text: t("connections.screen.expiredBanner")}
    : c.status === "degraded" ? {tone: "warning" as const, title: t("connections.screen.degradedTitle"), text: t("connections.screen.degradedBanner")}
      : merchant && days !== undefined && days <= 90 && expiry
        ? {tone: "warning" as const, title: t("connections.screen.renewSoonTitle"), text: t("connections.screen.renewBanner", {date: expiry})}
        : null

  return (
    <div className="space-y-8">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ConnectionStatusBadge status={c.status}/>
        {c.account_last4 && <span className="text-sm text-muted-foreground">{t("connections.screen.account", {last4: c.account_last4})}</span>}
        {c.kind === "sandbox_shared" && <span className="text-sm text-muted-foreground">{t("connections.sandbox.title")}</span>}
      </div>

      {banner && <Alert tone={banner.tone} title={banner.title}>{banner.text}</Alert>}
      {!c.backfill_done && <p className="text-sm text-muted-foreground">{t("connections.screen.backfilling")}</p>}

      <section aria-labelledby="conn-capabilities" className="space-y-3">
        <h2 id="conn-capabilities" className="text-base font-semibold text-foreground">{t("connections.screen.capabilities")}</h2>
        <ul className="flex flex-wrap gap-2">
          {(["statement", "collection"] as const).filter(k => c.capabilities.includes(k)).map(k => (
            <li key={k}><Badge tone="neutral"><CheckCircle2 aria-hidden/>{t(`connections.capabilities.${k}`)}</Badge></li>
          ))}
        </ul>
        {merchant && c.missing_scopes.length > 0 && (
          <p className="text-pretty text-sm text-muted-foreground">{t("connections.screen.missingScopes", {scopes: c.missing_scopes.join(", ")})}</p>
        )}
      </section>

      <section aria-labelledby="conn-health" className="space-y-3">
        <h2 id="conn-health" className="text-base font-semibold text-foreground">{t("connections.screen.health")}</h2>
        <dl className="divide-y divide-border border-y border-border text-sm">
          <Fact term={t("connections.screen.lastSync")}>
            {c.last_sync_ok_at ? when(c.last_sync_ok_at, i18n.language) : t("connections.screen.never")}
          </Fact>
          {c.first_day && c.backfill_done && <Fact term={t("connections.status.active")}>{t("connections.screen.statementFrom", {date: instantShortDate(`${c.first_day}T12:00:00Z`)})}</Fact>}
          {c.last_error_code && (
            <Fact term={t("connections.screen.lastError")}>
              {codeMessage(`byok_${c.last_error_code === "unavailable" ? "provider_unavailable" : c.last_error_code}`)}
              {c.last_error_at && <span className="text-muted-foreground"> • {when(c.last_error_at, i18n.language)}</span>}
            </Fact>
          )}
          {merchant && c.client_id && <Fact term={t("connections.screen.clientId")}><span className="break-all font-mono text-xs">{c.client_id}</span></Fact>}
          {merchant && c.notify_email && <Fact term={t("connections.screen.notifyEmail")}>{c.notify_email}</Fact>}
        </dl>
      </section>

      {merchant && expiry && (
        <section aria-labelledby="conn-cert" className="space-y-3">
          <h2 id="conn-cert" className="text-base font-semibold text-foreground">{t("connections.screen.certificate")}</h2>
          <dl className="divide-y divide-border border-y border-border text-sm">
            <Fact term={t("connections.screen.expires", {date: expiry})}>
              {days !== undefined && days > 0 ? t("connections.screen.daysLeft", {count: days}) : t("connections.status.expired")}
            </Fact>
            {c.cert_subject && <Fact term={t("connections.screen.subject")}><span className="break-all">{c.cert_subject}</span></Fact>}
            {c.cert_fingerprint && <Fact term={t("connections.screen.fingerprint")}><span className="break-all font-mono text-xs">{c.cert_fingerprint}</span></Fact>}
          </dl>
        </section>
      )}

      <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
        <Button variant="brand" disabled={sync.isPending} onClick={() => sync.mutate()}>{t("connections.screen.syncNow")}</Button>
        <Button variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>{t("connections.screen.test")}</Button>
        {merchant && <Button variant="outline" onClick={() => { renew.reset(); setRenewing(true) }}>{t("connections.screen.renew")}</Button>}
        {statementHref && c.ledger_account_id && (
          <Link href={statementHref} className={cn(buttonVariants({variant: "ghost"}))}>{t("connections.screen.openStatement")}</Link>
        )}
        <Button variant="ghost" className="text-danger sm:ml-auto" onClick={() => setLeaving(true)}>{t("connections.screen.disconnect")}</Button>
      </div>

      <RenewDialog open={renewing} onClose={() => setRenewing(false)} loading={renew.isPending}
        error={renew.isError ? words(renew.error) : undefined} onSubmit={(cert, key) => renew.mutate({cert, key})}/>
      <DisconnectDialog open={leaving} onClose={() => setLeaving(false)} loading={leave.isPending} onConfirm={() => leave.mutate()}/>
    </div>
  )
}

function Fact({term, children}: {term: string; children: React.ReactNode}) {
  return (
    <div className="grid gap-1 py-2.5 sm:grid-cols-[12rem_minmax(0,1fr)] sm:gap-4">
      <dt className="text-muted-foreground">{term}</dt>
      <dd className="min-w-0 text-foreground">{children}</dd>
    </div>
  )
}

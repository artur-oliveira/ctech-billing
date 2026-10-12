"use client"

import {Badge, Button, Modal} from "@aoctech/ui"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {messageFor} from "@/lib/api/client"
import type {ExcessPayment} from "@/lib/api/consoleTypes"
import {instantShortDate, money} from "@/lib/format"

const TONE = {open: "attention", refunding: "neutral", refunded: "positive", refund_failed: "urgent"} as const

/**
 * Money above what an invoice owed (D19): it is in the merchant's account and
 * stays there unless the merchant presses "Devolver", after a confirmation.
 */
export function ExcessPayments({rows, currency, onRefund}: {
  rows: ExcessPayment[]
  currency: string
  onRefund: (id: string) => Promise<ExcessPayment>
}) {
  const {t, i18n} = useTranslation()
  const [local, setLocal] = useState<Record<string, ExcessPayment>>({})
  const [asking, setAsking] = useState<ExcessPayment | null>(null)
  const [busy, setBusy] = useState(false)
  if (rows.length === 0) return null
  const list = rows.map(r => local[r.id] ?? r)

  async function refund() {
    if (!asking) return
    setBusy(true)
    try {
      const next = await onRefund(asking.id)
      setLocal(m => ({...m, [next.id]: next}))
      toast.success(t("connections.pix.refundStarted"))
      setAsking(null)
    } catch (e) {
      toast.error(messageFor(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="excess" className="space-y-3">
      <h2 id="excess" className="text-sm font-medium text-muted-foreground">{t("connections.pix.excessTitle")}</h2>
      <ul className="divide-y divide-border border-y border-border">
        {list.map(r => (
          <li key={r.id} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0 space-y-1">
              <p className="text-pretty text-sm text-foreground">
                {r.kind === "credit_refund" ? t("connections.pix.creditRefund") : t("connections.pix.excessBody", {amount: money(r.amount, currency)})}
              </p>
              <p data-numeric className="text-xs text-muted-foreground">{instantShortDate(r.paid_at)} · {money(r.amount, currency)}</p>
            </div>
            <div className="flex items-center gap-2">
              <Badge tone={TONE[r.status]}>{t(`connections.pix.excessStatus.${r.status}`)}</Badge>
              {(r.status === "refund_failed" || (r.status === "open" && r.kind === "excess")) && (
                <Button variant="outline" size="sm" onClick={() => setAsking(r)}>{t("connections.pix.refund")}</Button>
              )}
            </div>
          </li>
        ))}
      </ul>
      <Modal open={asking !== null} onClose={() => setAsking(null)} locale={i18n.language}
        title={asking ? t("connections.pix.refundTitle", {amount: money(asking.amount, currency)}) : ""}
        description={t("connections.pix.refundBody")} danger loading={busy}
        submitLabel={t("connections.pix.refundConfirm")} cancelLabel={t("connections.pix.refundKeep")} onSubmit={() => void refund()}/>
    </section>
  )
}

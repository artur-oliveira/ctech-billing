"use client"

import {Button} from "@aoctech/ui"
import {QRCodeSVG} from "qrcode.react"
import {Copy, FlaskConical, RefreshCw} from "lucide-react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import type {InvoiceByok} from "@/lib/api/consoleTypes"
import {codeMessage} from "@/lib/errors"
import {money, shortDate} from "@/lib/format"

/**
 * The invoice's Pix with due date (BYOK spec § 7.2, § 11.3): the QR and the
 * code to copy, the due date and the last day it is payable; in test mode the
 * sandbox payment; when there is no Pix, why, in words, and the way out.
 * On a phone the QR sits centred above the code; side by side from sm.
 */
export function InvoicePix({byok, currency, onSimulate, onNewPix, busy}: {
  byok: InvoiceByok
  currency: string
  onSimulate: () => void
  onNewPix: () => void
  busy: boolean
}) {
  const {t} = useTranslation()
  const bank = t(`connections.providers.${byok.provider ?? "inter"}`)

  if (byok.attempt_status === "SUCCEEDED") {
    return (
      <p className="text-sm text-foreground">
        {t("connections.pix.paid", {bank})}
        {byok.late_charges ? <span className="text-muted-foreground"> {t("connections.pix.lateCharges", {amount: money(byok.late_charges, currency)})}</span> : null}
      </p>
    )
  }

  if (!byok.payable) {
    return (
      <div className="space-y-3">
        <p className="text-pretty text-sm text-muted-foreground">
          {byok.reason ? codeMessage(byok.reason) : t("connections.pix.none")}
        </p>
        {byok.can_retry && (
          <Button variant="outline" size="sm" onClick={onNewPix} disabled={busy}>
            <RefreshCw aria-hidden className="size-3.5"/>{t("connections.pix.newPix")}
          </Button>
        )}
      </div>
    )
  }

  const code = byok.payable
  return (
    <div className="flex flex-col items-center gap-5 sm:flex-row sm:items-start">
      <QRCodeSVG value={code} size={200} level="M" marginSize={2} bgColor="#ffffff" fgColor="#211816"
        title={t("connections.pix.qrTitle")} className="shrink-0 rounded-md border border-border"/>
      <div className="w-full min-w-0 space-y-3">
        <dl className="space-y-1 text-sm">
          {byok.due_date && <dd data-numeric className="text-foreground">{t("connections.pix.due", {date: shortDate(byok.due_date)})}</dd>}
          {byok.payable_until && <dd data-numeric className="text-muted-foreground">{t("connections.pix.payUntil", {date: shortDate(byok.payable_until)})}</dd>}
        </dl>
        <p className="break-all rounded-md bg-surface px-3 py-2 font-mono text-xs text-foreground">{code}</p>
        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
          <Button variant="outline" size="sm" onClick={async () => {
            try {
              await navigator.clipboard.writeText(code)
              toast.success(t("connections.pix.copied"))
            } catch {
              toast.error(t("connections.pix.copyFailed"))
            }
          }}>
            <Copy aria-hidden className="size-3.5"/>{t("connections.pix.copy")}
          </Button>
          {byok.simulate && (
            <Button variant="ghost" size="sm" onClick={onSimulate} disabled={busy}>
              <FlaskConical aria-hidden className="size-3.5"/>{t("connections.pix.simulate")}
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

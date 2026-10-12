"use client"

import {Modal} from "@aoctech/ui"
import {useTranslation} from "react-i18next"

import type {DisconnectReport} from "@/lib/api/connections"

/**
 * Disconnect, confirmed (BYOK spec § 3.6): CTech deletes the credentials and
 * stops syncing, the history stays; and only the merchant can cancel the
 * integration at Inter, which the dialog says before anything happens. With
 * open charges it says how many it cancels; when Inter is down it offers
 * "Desconectar mesmo assim" and, after it, what to remove at the bank.
 */
export function DisconnectDialog({open, onClose, onConfirm, loading, openCharges, unreachable, report}: {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  loading: boolean
  openCharges?: number
  unreachable?: boolean
  report?: DisconnectReport | null
}) {
  const {t, i18n} = useTranslation()
  const submit = report ? undefined : unreachable ? t("connections.disconnect.anyway")
    : openCharges ? t("connections.disconnect.cancelAndLeave") : t("connections.disconnect.confirm")
  return (
    <Modal open={open} onClose={onClose} title={t("connections.disconnect.title")} danger locale={i18n.language}
      submitLabel={submit} cancelLabel={t("connections.disconnect.cancel")} onSubmit={report ? undefined : onConfirm} loading={loading}>
      <div className="space-y-3 text-sm">
        {report ? (
          <>
            <p className="font-medium text-foreground">{t("connections.disconnect.leftoverTitle")}</p>
            {report.charge_refs.length > 0 && (
              <p className="break-all font-mono text-xs text-foreground">{t("connections.disconnect.leftoverCharges", {refs: report.charge_refs.join(", ")})}</p>
            )}
            {report.webhook_url && <p className="break-all text-xs text-foreground">{t("connections.disconnect.leftoverWebhook", {url: report.webhook_url})}</p>}
          </>
        ) : (
          <>
            {openCharges ? <p className="text-pretty font-medium text-foreground">{t("connections.disconnect.openCharges", {count: openCharges})}</p> : null}
            {unreachable && <p className="text-pretty text-foreground">{t("connections.disconnect.anywayBody")}</p>}
            <p className="text-pretty text-foreground">{t("connections.disconnect.body")}</p>
          </>
        )}
        <p className="text-pretty text-muted-foreground">{t("connections.disconnect.onlyYou")}</p>
      </div>
    </Modal>
  )
}

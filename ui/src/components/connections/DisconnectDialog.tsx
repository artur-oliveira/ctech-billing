"use client"

import {Modal} from "@aoctech/ui"
import {useTranslation} from "react-i18next"

/**
 * Disconnect, confirmed (BYOK spec § 3.6): CTech deletes the credentials and
 * stops syncing, the history stays; and only the merchant can cancel the
 * integration at Inter, which the dialog says before anything happens.
 */
export function DisconnectDialog({open, onClose, onConfirm, loading}: {open: boolean; onClose: () => void; onConfirm: () => void; loading: boolean}) {
  const {t, i18n} = useTranslation()
  return (
    <Modal open={open} onClose={onClose} title={t("connections.disconnect.title")} danger locale={i18n.language}
      submitLabel={t("connections.disconnect.confirm")} cancelLabel={t("connections.disconnect.cancel")} onSubmit={onConfirm} loading={loading}>
      <div className="space-y-3 text-sm">
        <p className="text-pretty text-foreground">{t("connections.disconnect.body")}</p>
        <p className="text-pretty text-muted-foreground">{t("connections.disconnect.onlyYou")}</p>
      </div>
    </Modal>
  )
}

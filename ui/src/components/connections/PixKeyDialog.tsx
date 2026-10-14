"use client"

import {Field, Input, Modal} from "@aoctech/ui"
import {useState} from "react"
import {useTranslation} from "react-i18next"

/**
 * "Alterar chave Pix": the key the collection charges are issued on. It is not
 * part of the sealed credentials, so nothing but the key is asked. Inter keeps
 * one payment-notice address per key, which is why the copy recommends a random
 * (EVP) key of CTech's own instead of one another system already uses.
 */
export function PixKeyDialog({open, onClose, onSubmit, loading, current, error}: {
  open: boolean
  onClose: () => void
  onSubmit: (pixKey: string) => void
  loading: boolean
  current?: string
  error?: string
}) {
  const {t, i18n} = useTranslation()
  const [value, setValue] = useState("")
  const next = value.trim()
  const unchanged = next === "" || next === current
  return (
    <Modal open={open} onClose={onClose} title={t("connections.pixKey.title")} description={t("connections.pixKey.body")}
      locale={i18n.language} submitLabel={t("connections.pixKey.submit")} submitDisabled={unchanged} loading={loading}
      onSubmit={() => { if (!unchanged) onSubmit(next) }}>
      <div className="grid gap-4">
        <Field label={t("connections.pixKey.label")} htmlFor="pix-key-new" hint={current ? t("connections.pixKey.current", {key: current}) : undefined}>
          <Input id="pix-key-new" value={value} onChange={e => setValue(e.target.value)} autoComplete="off" spellCheck={false}/>
        </Field>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
      </div>
    </Modal>
  )
}

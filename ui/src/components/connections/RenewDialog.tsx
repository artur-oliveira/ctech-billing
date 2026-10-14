"use client";

import {FileDrop, Modal} from "@aoctech/ui";
import {useState} from "react";
import {useTranslation} from "react-i18next";

/**
 * Certificate renewal (BYOK spec § 3.5): the new .crt and .key only. The
 * client_id and client_secret stay where they are, sealed; the api never sees
 * the secret again.
 */
export function RenewDialog({open, onClose, onSubmit, loading, error}: {
  open: boolean
  onClose: () => void
  onSubmit: (cert: File, key: File) => void
  loading: boolean
  error?: string
}) {
  const {t, i18n} = useTranslation();
  const [cert, setCert] = useState<File | null>(null);
  const [key, setKey] = useState<File | null>(null);
  const labels = {
    chooseLabel: t("connections.form.choose"), replaceLabel: t("connections.form.replace"), removeLabel: t("connections.form.remove"),
    tooLargeLabel: t("connections.form.tooLarge"), wrongTypeLabel: t("connections.form.wrongType"),
  };
  return (
    <Modal open={open} onClose={onClose} title={t("connections.screen.renewTitle")} description={t("connections.screen.renewBody")}
      locale={i18n.language} submitLabel={t("connections.screen.renewSubmit")} submitDisabled={!cert || !key} loading={loading}
      onSubmit={() => { if (cert && key) onSubmit(cert, key); }}>
      <div className="grid gap-4">
        <FileDrop id="renew-cert" label={t("connections.form.cert")} accept=".crt,.pem,.cer" maxBytes={16384} file={cert} onFile={setCert} {...labels}/>
        <FileDrop id="renew-key" label={t("connections.form.key")} accept=".key,.pem" maxBytes={16384} file={key} onFile={setKey} {...labels}/>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
      </div>
    </Modal>
  );
}

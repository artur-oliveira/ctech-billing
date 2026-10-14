"use client";

import {Alert, Button, Modal} from "@aoctech/ui";
import type {AxiosError} from "axios";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {messageFor, problemCode} from "@/lib/api/client";

/**
 * "Webhook de pagamentos" (BYOK spec § 6.4, § 11.1 step 6): registered, absent
 * (confirmations within the hour), or another system's. Another system's
 * notice is replaced only after a confirmation that names it.
 */
export function WebhookStep({status, onRegister}: {
  status?: string
  onRegister: (replaceExisting: boolean) => Promise<unknown>
}) {
  const {t, i18n} = useTranslation();
  const [host, setHost] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  async function register(replace: boolean) {
    setBusy(true);
    try {
      await onRegister(replace);
      setHost(null);
      setConfirming(false);
      toast.success(t("connections.webhook.done"));
    } catch (e) {
      if (problemCode(e) === "byok_webhook_conflict") {
        setHost((e as AxiosError<{existing_host?: string}>).response?.data?.existing_host ?? "?");
      } else {
        toast.error(messageFor(e));
      }
    } finally {
      setBusy(false);
    }
  }

  const conflict = host ?? (status === "conflict" ? "?" : null);
  const line = status === "registered" ? t("connections.webhook.registered")
    : status === "overwritten" ? t("connections.webhook.overwritten") : t("connections.webhook.notRegistered");
  return (
    <section aria-labelledby="conn-webhook" className="space-y-3">
      <h2 id="conn-webhook" className="text-base font-semibold text-foreground">{t("connections.webhook.title")}</h2>
      {conflict ? (
        <Alert tone="warning" title={t("connections.webhook.conflictTitle")}>{t("connections.webhook.conflictBody", {host: conflict})}</Alert>
      ) : (
        <p className="text-pretty text-sm text-muted-foreground">{line}</p>
      )}
      <div className="flex flex-col gap-2 sm:flex-row">
        <Button variant="outline" disabled={busy} onClick={() => void register(false)}>{t("connections.webhook.register")}</Button>
        {conflict && <Button variant="ghost" disabled={busy} onClick={() => setConfirming(true)}>{t("connections.webhook.replace")}</Button>}
      </div>
      <Modal open={confirming} onClose={() => setConfirming(false)} locale={i18n.language} danger loading={busy}
        title={t("connections.webhook.replaceTitle", {host: conflict ?? ""})} description={t("connections.webhook.replaceBody", {host: conflict ?? ""})}
        submitLabel={t("connections.webhook.replace")} cancelLabel={t("connections.webhook.replaceKeep")} onSubmit={() => void register(true)}/>
    </section>
  );
}

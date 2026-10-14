"use client";

import {Button, Modal} from "@aoctech/ui";
import {Copy} from "lucide-react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

/**
 * The webhook signing secret, shown exactly once (spec § 5.2). The parent
 * holds it in state only while this is open and drops it on `onDone`; nothing
 * caches it, and no list ever returns it again. The only way out says what the
 * person did ("Já copiei"); the close button and Escape do the same.
 */
export function SecretReveal({secret, onDone}: {secret: string | null; onDone: () => void}) {
  const {t, i18n} = useTranslation();
  async function copy() {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      toast.success(t("console.webhooks.secret.copied"));
    } catch {
      toast.error(t("console.webhooks.secret.copyFailed"));
    }
  }
  return (
    <Modal
      open={secret !== null} onClose={onDone} locale={i18n.language}
      title={t("console.webhooks.secret.title")} description={t("console.webhooks.secret.body")}
      cancelLabel={t("console.webhooks.secret.done")}
    >
      {/* A textarea, not an input: the whole secret is visible (it wraps),
          so a person copying by hand never copies a cut-off value. */}
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
        <textarea
          readOnly rows={2} aria-label={t("console.webhooks.secret.label")} value={secret ?? ""}
          spellCheck={false} onFocus={e => e.currentTarget.select()}
          className="min-w-0 resize-none rounded-md border border-border bg-surface px-3 py-2 font-mono text-xs leading-relaxed break-all text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/35 sm:flex-1"
        />
        <Button variant="outline" className="gap-1.5 sm:shrink-0" onClick={() => void copy()}>
          <Copy aria-hidden className="size-3.5"/>
          {t("console.webhooks.secret.copy")}
        </Button>
      </div>
    </Modal>
  );
}

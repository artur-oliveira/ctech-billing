"use client";

import {Field, Modal} from "@aoctech/ui";
import {useMutation, useQueryClient} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {Textarea} from "@/components/ui/Textarea";
import {messageFor} from "@/lib/api/client";
import {rejectApplication} from "@/lib/api/console";
import type {MerchantApplication} from "@/lib/api/consoleTypes";
import {rejectReasonErrors} from "@/lib/console/applications";
import {useConsoleScope} from "@/lib/console/scope";
import {fieldErrorMessage} from "@/lib/errors";
import limits from "@/lib/limits.json";

/** Rejecting needs a reason the applicant will read (spec § 4.2: 10 to 500). */
export function RejectDialog({application, open, onClose}: {application: MerchantApplication; open: boolean; onClose: () => void}) {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const reject = useMutation({
    mutationFn: () => rejectApplication(scope, application.id, reason.trim()),
    onSuccess: () => {
      toast.success(t("console.applications.rejected_toast", {name: application.organization_name}));
      void queryClient.invalidateQueries({queryKey: ["console", scope.org, scope.mode, "applications"] as const});
      onClose();
    },
    onError: e => setError(messageFor(e)),
  });
  return (
    <Modal
      open={open}
      onClose={onClose}
      danger
      title={t("console.applications.rejectTitle", {name: application.organization_name})}
      description={t("console.applications.rejectBody")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.applications.rejectSubmit")}
      loading={reject.isPending}
      onSubmit={() => {
        const errs = rejectReasonErrors(reason);
        if (errs.length) { setError(fieldErrorMessage(errs[0])); return; }
        setError("");
        reject.mutate();
      }}
    >
      <Field label={t("console.applications.rejectReason")} htmlFor="reject-reason" hint={t("console.applications.rejectHint")} error={error || undefined}>
        <Textarea id="reject-reason" maxLength={limits.text.reason} value={reason}
          aria-invalid={!!error || undefined} onChange={e => { setReason(e.target.value); setError(""); }}/>
      </Field>
    </Modal>
  );
}

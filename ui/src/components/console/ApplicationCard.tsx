"use client";

import {Button, Modal} from "@aoctech/ui";
import {useMutation, useQueryClient} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {RejectDialog} from "@/components/console/RejectDialog";
import {messageFor} from "@/lib/api/client";
import {approveApplication} from "@/lib/api/console";
import type {MerchantApplication} from "@/lib/api/consoleTypes";
import {useConsoleScope} from "@/lib/console/scope";
import {instantDate} from "@/lib/format";

/**
 * One application, as tenant zero's reviewers decide on it (spec § 7.3). A
 * ruled block, not a bordered card: blocks on this screen are separated by
 * hairlines (DESIGN.md "One raised thing per screen, or none"). The actions
 * share a full-width row at the bottom on a phone and sit in the header from sm.
 */
export function ApplicationCard({application: a}: {application: MerchantApplication}) {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [rejecting, setRejecting] = useState(false);
  const approve = useMutation({
    mutationFn: () => approveApplication(scope, a.id),
    onSuccess: () => {
      toast.success(t("console.applications.approved", {name: a.organization_name}));
      void queryClient.invalidateQueries({queryKey: ["console", scope.org, scope.mode, "applications"]});
      setConfirming(false);
    },
    onError: e => toast.error(messageFor(e)),
  });
  const pending = a.status === "pending";

  const actions = pending && (
    <>
      <Button variant="outline" onClick={() => setRejecting(true)}>{t("console.applications.reject")}</Button>
      <Button variant="brand" onClick={() => setConfirming(true)}>{t("console.applications.approve")}</Button>
    </>
  );

  return (
    // One column on a phone with the actions last (a full-width row); from sm a
    // two-column grid whose first row is the name and, on the right, the actions.
    <article className="grid gap-3 border-b border-border py-5 first:pt-0 sm:grid-cols-[minmax(0,1fr)_auto] sm:gap-x-6 sm:[&>*:not(header):not([data-actions])]:col-span-2">
      <header className="min-w-0">
        <div className="min-w-0">
          <h2 className="text-base font-semibold text-foreground">{a.organization_name}</h2>
          <p className="text-xs text-muted-foreground">
            {pending || !a.decided_at
              ? t("console.applications.appliedOn", {date: instantDate(a.applied_at)})
              : t("console.applications.decidedOn", {date: instantDate(a.decided_at)})}
          </p>
        </div>
      </header>
      <p className="text-sm text-foreground">
        <span className="font-medium">{a.company.legal_name}</span>
        <span className="text-muted-foreground"> • </span>
        <span data-numeric>{a.company.tax_id}</span>
      </p>
      <p className="max-w-prose text-sm text-foreground">{a.description}</p>
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
        {a.website && (
          <div className="min-w-0"><dt className="text-xs text-muted-foreground">{t("console.applications.website")}</dt><dd className="break-all">{a.website}</dd></div>
        )}
        {a.expected_monthly_volume && (
          <div><dt className="text-xs text-muted-foreground">{t("console.applications.volume")}</dt><dd>{t(`console.volume.${a.expected_monthly_volume}`)}</dd></div>
        )}
        <div className="min-w-0"><dt className="text-xs text-muted-foreground">{t("console.applications.contact")}</dt><dd className="break-all">{a.contact_email}</dd></div>
        {a.payout && (
          <div className="min-w-0">
            <dt className="text-xs text-muted-foreground">{t("console.applications.payout")}</dt>
            <dd>
              {a.payout.rail === "byok"
                ? t("console.applications.railByok", {bank: t(`connections.providers.${a.payout.provider ?? "inter"}`)})
                : t("console.applications.railWallet")}
              {" · "}{t(`console.applications.payoutStatus.${a.payout.status}`, {defaultValue: a.payout.status})}
            </dd>
          </div>
        )}
      </dl>
      {a.rejection_reason && (
        <p className="text-sm text-muted-foreground"><span className="font-medium text-foreground">{t("console.applications.reason")}: </span>{a.rejection_reason}</p>
      )}
      {/* Last in reading order (a phone shows them last); from sm the grid
          lifts them into the first row, beside the name. */}
      {actions && (
        <div data-actions className="grid grid-cols-2 gap-2 pt-1 sm:col-start-2 sm:row-start-1 sm:flex sm:pt-0">
          {actions}
        </div>
      )}
      <Modal
        open={confirming}
        onClose={() => setConfirming(false)}
        title={t("console.applications.approveTitle", {name: a.organization_name})}
        description={t("console.applications.approveBody")}
        cancelLabel={t("common.cancel")}
        submitLabel={t("console.applications.approve")}
        loading={approve.isPending}
        onSubmit={() => approve.mutate()}
      />
      <RejectDialog application={a} open={rejecting} onClose={() => setRejecting(false)}/>
    </article>
  );
}

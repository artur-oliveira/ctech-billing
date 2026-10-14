"use client";

import {useQuery} from "@tanstack/react-query";
import Link from "next/link";
import {useTranslation} from "react-i18next";

import {consoleKeys, getMerchantApplication} from "@/lib/api/console";
import {tenantInMode} from "@/lib/console/merchant";
import {useMerchantSelection} from "@/lib/console/selection";

/**
 * One line under the header for an organization whose billing is not simply
 * live (spec § 7.2): live under review, live refused (with its reason), or
 * suspended. Silent where the invitation already says it (no tenant in this
 * mode) and for a live merchant.
 */
export function MerchantStatusBand() {
  const {t} = useTranslation();
  const {scope, organization} = useMerchantSelection();
  const status = organization.merchant_status;
  // Under review only with a pending application (a hand-provisioned test
  // tenant has none, and nobody is reviewing it).
  const pending = status === "test" && organization.application_status === "pending";
  const visible = status === "suspended" || ((pending || status === "rejected") && tenantInMode(status, scope.mode));
  const {data: application} = useQuery({
    queryKey: consoleKeys.merchantApplication(scope),
    queryFn: () => getMerchantApplication(scope),
    enabled: visible && status === "rejected",
  });
  if (!visible) return null;

  const tone = status === "suspended" ? "bg-danger/10 border-danger/30" : "bg-surface border-border";
  return (
    <div role={status === "suspended" ? "alert" : "status"} className={`border-b ${tone}`}>
      <div className="mx-auto flex max-w-6xl flex-col gap-1 px-4 py-2 text-sm text-foreground sm:flex-row sm:items-center sm:justify-between sm:gap-4">
        <p>
          {status === "suspended" && t("console.band.suspended", {name: organization.display_name})}
          {pending && t("console.band.pending")}
          {status === "rejected" && application?.rejection_reason && t("console.band.rejected", {reason: application.rejection_reason})}
        </p>
        {status === "rejected" && (
          <Link href="/console/apply" className="shrink-0 font-medium text-brand-600 underline-offset-4 hover:underline touch:min-h-11 touch:inline-flex touch:items-center">
            {t("console.band.applyAgain")}
          </Link>
        )}
      </div>
    </div>
  );
}

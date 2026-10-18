"use client";

import {PageHeader} from "@aoctech/ui";
import {useSearchParams} from "next/navigation";
import {Suspense} from "react";
import {useTranslation} from "react-i18next";

import {ReportsView} from "@/components/finance/ReportsView";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** F7 — By category (the default), monthly review, cash flow, card spending and the DRE. `?view=` opens on that report. */
export default function FinanceReportsPage() {
  const {t} = useTranslation();
  useDocumentTitle(t("finance.nav.reports"));
  return (
    <div className="space-y-6">
      <PageHeader title={t("finance.nav.reports")}/>
      <Suspense>
        <Reports/>
      </Suspense>
    </div>
  );
}

function Reports() {
  const asked = useSearchParams().get("view");
  const view = asked === "dre" || asked === "cash" || asked === "cards" || asked === "review" || asked === "category" ? asked : "category";
  return <ReportsView view={view}/>;
}

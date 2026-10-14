"use client";

import {PageHeader} from "@aoctech/ui";
import {useSearchParams} from "next/navigation";
import {Suspense} from "react";
import {useTranslation} from "react-i18next";

import {StatementView} from "@/components/finance/StatementView";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** F3 — an account's statement. `?account=` picks the account; none, the first. */
export default function FinanceStatementPage() {
  const {t} = useTranslation();
  useDocumentTitle(t("finance.nav.statement"));
  return (
    <div className="space-y-6">
      <PageHeader title={t("finance.nav.statement")}/>
      <Suspense>
        <Statement/>
      </Suspense>
    </div>
  );
}

function Statement() {
  const account = useSearchParams().get("account") ?? "";
  return <StatementView key={account} account={account}/>;
}

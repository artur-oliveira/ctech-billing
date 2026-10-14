"use client";

import {useSearchParams} from "next/navigation";
import {Suspense} from "react";
import {useTranslation} from "react-i18next";

import {UnclassifiedView} from "@/components/finance/UnclassifiedView";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** A bank-synced account's lines to classify (BYOK spec § 8.3). `?account=` names it. */
export default function FinanceUnclassifiedPage() {
  const {t} = useTranslation();
  useDocumentTitle(t("connections.unclassified.title"));
  return (
    <Suspense>
      <Unclassified/>
    </Suspense>
  );
}

function Unclassified() {
  const account = useSearchParams().get("account") ?? "";
  return <UnclassifiedView key={account} account={account}/>;
}

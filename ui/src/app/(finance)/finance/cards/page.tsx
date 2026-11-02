"use client";

import {PageHeader} from "@aoctech/ui";
import {useSearchParams} from "next/navigation";
import {Suspense} from "react";
import {useTranslation} from "react-i18next";

import {CardsView} from "@/components/finance/CardsView";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** F5 — cards and their statements. `?card=` picks the card; none, the first. `?month=` the statement shown; none, the open one. */
export default function FinanceCardsPage() {
  const {t} = useTranslation();
  useDocumentTitle(t("finance.nav.cards"));
  return (
    <div className="space-y-6">
      <PageHeader title={t("finance.nav.cards")}/>
      <Suspense>
        <Cards/>
      </Suspense>
    </div>
  );
}

function Cards() {
  const params = useSearchParams();
  const card = params.get("card") ?? "";
  const month = params.get("month") ?? "";
  return <CardsView key={`${card}#${month}`} card={card} month={/^\d{4}-\d{2}$/.test(month) ? month : undefined}/>;
}

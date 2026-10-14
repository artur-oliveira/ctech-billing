"use client";

import {useTranslation} from "react-i18next";

import {PlanView} from "@/components/finance/PlanView";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** Finanças → Plano (spec § 7): the person's plan, from Pessoal. */
export default function FinancePlanPage() {
  const {t} = useTranslation();
  useDocumentTitle(t("finance.nav.plan"));
  return <PlanView/>;
}

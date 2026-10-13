"use client"

import {useTranslation} from "react-i18next"

import {BillingPlanView} from "@/components/console/BillingPlanView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** Console → Plano (BYOK part C): the organization's plan and its use. */
export default function ConsolePlanPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("console.plan.title"))
  return <BillingPlanView/>
}

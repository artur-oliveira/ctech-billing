"use client"

import {useTranslation} from "react-i18next"

import {OverviewView} from "@/components/finance/OverviewView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F1 — the finance overview. */
export default function FinanceOverviewPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.overview"))
  return <OverviewView/>
}

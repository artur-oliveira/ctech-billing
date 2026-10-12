"use client"

import {useTranslation} from "react-i18next"

import {ActionInboxView} from "@/components/finance/ActionInboxView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

export default function FinanceInboxPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.inbox.title"))
  return <ActionInboxView/>
}

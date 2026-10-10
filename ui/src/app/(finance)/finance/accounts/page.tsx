"use client"

import {useTranslation} from "react-i18next"

import {AccountsView} from "@/components/finance/AccountsView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F8 — accounts, categories and the default receiving account. */
export default function FinanceAccountsPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.accounts"))
  return <AccountsView/>
}

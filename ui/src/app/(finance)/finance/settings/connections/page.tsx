"use client"

import {useTranslation} from "react-i18next"

import {ConnectionsList} from "@/components/connections/ConnectionsList"
import {FinanceConnectionsGate} from "@/components/connections/FinanceConnectionsGate"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** The organization's bank connections, in Finanças (BYOK spec § 11). */
export default function FinanceConnectionsPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("connections.title"))
  return <FinanceConnectionsGate>{scope => <ConnectionsList scope={scope} base="/finance/settings/connections"/>}</FinanceConnectionsGate>
}

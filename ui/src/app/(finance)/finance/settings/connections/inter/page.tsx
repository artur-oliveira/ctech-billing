"use client"

import {useTranslation} from "react-i18next"

import {FinanceConnectionsGate} from "@/components/connections/FinanceConnectionsGate"
import {InterConnectionScreen} from "@/components/connections/InterConnectionScreen"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** Banco Inter in Finanças: the same screen as the console's (BYOK spec § 11). */
export default function FinanceInterConnectionPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("connections.providers.inter"))
  return (
    <FinanceConnectionsGate>
      {scope => <InterConnectionScreen scope={scope} backHref="/finance/settings/connections"
        statementHref={id => `/finance/statement?account=${encodeURIComponent(id)}`}/>}
    </FinanceConnectionsGate>
  )
}

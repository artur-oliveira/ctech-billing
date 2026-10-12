"use client"

import {useTranslation} from "react-i18next"

import {InterConnectionScreen} from "@/components/connections/InterConnectionScreen"
import {useConsoleScope} from "@/lib/console/scope"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** Banco Inter: onboarding, then the connection's health (BYOK spec § 11.1, § 11.2). */
export default function ConsoleInterConnectionPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("connections.providers.inter"))
  return <InterConnectionScreen scope={useConsoleScope()} backHref="/console/connections"/>
}

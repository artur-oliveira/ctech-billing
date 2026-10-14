"use client";

import {useTranslation} from "react-i18next";

import {ConsoleCollection} from "@/components/collection/ConsoleCollection";
import {InterConnectionScreen} from "@/components/connections/InterConnectionScreen";
import {useConsoleScope} from "@/lib/console/scope";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** Banco Inter: onboarding, then the connection's health (BYOK spec § 11.1, § 11.2). */
export default function ConsoleInterConnectionPage() {
  const {t} = useTranslation();
  useDocumentTitle(t("connections.providers.inter"));
  const scope = useConsoleScope();
  return (
    <InterConnectionScreen scope={scope} backHref="/console/connections"
      extra={c => c.capabilities.includes("collection") ? <ConsoleCollection scope={scope} provider={c.provider}/> : null}/>
  );
}

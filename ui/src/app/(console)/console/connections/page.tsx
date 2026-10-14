"use client";

import {useTranslation} from "react-i18next";

import {ConnectionsList} from "@/components/connections/ConnectionsList";
import {useConsoleScope} from "@/lib/console/scope";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** The organization's bank connections (BYOK spec § 11), in the console. */
export default function ConsoleConnectionsPage() {
  const {t} = useTranslation();
  useDocumentTitle(t("connections.title"));
  return <ConnectionsList scope={useConsoleScope()} base="/console/connections"/>;
}

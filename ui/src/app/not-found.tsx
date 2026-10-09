"use client"

import {Button} from "@aoctech/ui"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {StatusScreen} from "@/components/StatusScreen"

/**
 * 404. Almost always a mistyped or expired link rather than a bug, so it says
 * that plainly and offers the one destination that is certainly there.
 * Client component so it can translate; the tab title comes from the root layout.
 */
export default function NotFound() {
  const {t} = useTranslation()
  return (
    <StatusScreen
      title={t("auth.notFound.title")}
      description={t("auth.notFound.description")}
      action={<Button render={<Link href="/"/>}>{t("auth.notFound.home")}</Button>}
    />
  )
}

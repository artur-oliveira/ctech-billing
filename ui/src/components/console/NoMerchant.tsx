"use client"

import {EmptyState} from "@aoctech/ui"
import {Store} from "lucide-react"
import {useTranslation} from "react-i18next"

/**
 * An organization the person administers that has no billing tenant in this
 * mode (`no_merchant`). A beginning, not a fault: it is never drawn as an
 * error. Part 2 turns it into the invitation to apply.
 */
export function NoMerchant({name}: {name: string}) {
  const {t} = useTranslation()
  return (
    <EmptyState
      icon={<Store/>}
      title={t("console.noMerchant.title")}
      description={t("console.noMerchant.body", {name})}
      className="max-sm:items-start max-sm:text-left"
    />
  )
}

"use client"

import {Button, buttonVariants} from "@aoctech/ui"
import {Check} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {setMode} from "@/lib/console/mode"
import {useMerchantSelection} from "@/lib/console/selection"

/**
 * What an organization without a tenant in this mode sees (spec § 7.1): the
 * invitation to apply, or, once it applied, where its live mode stands. A
 * beginning, never drawn as an error.
 */
export function MerchantInvitation() {
  const {t} = useTranslation()
  const {organization} = useMerchantSelection()
  const name = organization.display_name
  const status = organization.merchant_status

  if (status === "test" || status === "rejected") {
    const key = status === "test" ? "pending" : "rejected"
    return (
      <section className="max-w-xl space-y-3">
        <h1 className="text-xl font-semibold text-foreground">{t(`console.invitation.${key}Title`)}</h1>
        <p className="text-sm text-muted-foreground">
          {status === "test" ? t("console.invitation.pendingBody", {name}) : t("console.invitation.rejectedBody")}
        </p>
        <div className="flex flex-col gap-2 pt-2 sm:flex-row">
          <Button variant={status === "test" ? "brand" : "outline"} className="max-sm:w-full" onClick={() => setMode("test")}>
            {t("console.invitation.toTest")}
          </Button>
          {status === "rejected" && (
            <Link href="/console/apply" className={buttonVariants({variant: "brand", className: "max-sm:w-full"})}>
              {t("console.invitation.applyAgain")}
            </Link>
          )}
        </div>
      </section>
    )
  }

  return (
    <section className="max-w-xl space-y-4">
      <h1 className="text-2xl font-semibold tracking-[-0.02em] text-foreground">{t("console.invitation.title")}</h1>
      <p className="text-sm text-muted-foreground">{t("console.invitation.body", {name})}</p>
      <ul className="space-y-2">
        {(["subscriptions", "api", "test"] as const).map(p => (
          <li key={p} className="flex items-start gap-2 text-sm text-foreground">
            <Check aria-hidden className="mt-0.5 size-4 shrink-0 text-brand-600"/>
            {t(`console.invitation.points.${p}`)}
          </li>
        ))}
      </ul>
      <div className="space-y-2 pt-2">
        <Link href="/console/apply" className={buttonVariants({variant: "brand", className: "max-sm:w-full"})}>
          {t("console.invitation.action")}
        </Link>
        <p className="text-xs text-muted-foreground">{t("console.invitation.review")}</p>
      </div>
    </section>
  )
}

"use client"

import {Button} from "@aoctech/ui"
import {useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

import type {MerchantApplication} from "@/lib/api/consoleTypes"
import {setMode} from "@/lib/console/mode"
import {instantDate} from "@/lib/format"

/** After applying (spec § 7.1): test mode is ready, live is under review. */
export function ApplicationSubmitted({application}: {application: MerchantApplication}) {
  const {t} = useTranslation()
  const router = useRouter()
  return (
    <section className="max-w-xl space-y-3" aria-live="polite">
      <h1 className="text-xl font-semibold text-foreground">{t("console.apply.submittedTitle")}</h1>
      <p className="text-sm text-foreground">{t("console.apply.submittedBody", {name: application.organization_name})}</p>
      <p className="text-xs text-muted-foreground">{t("console.apply.sentOn", {date: instantDate(application.applied_at)})}</p>
      <Button variant="brand" className="max-sm:w-full" onClick={() => { setMode("test"); router.push("/console/overview") }}>
        {t("console.apply.openTest")}
      </Button>
    </section>
  )
}

"use client"

import {Button, ErrorState, PageHeader, Skeleton} from "@aoctech/ui"
import {useQuery, useQueryClient} from "@tanstack/react-query"
import {useRouter} from "next/navigation"
import {useEffect} from "react"
import {useTranslation} from "react-i18next"

import {ApplicationSubmitted} from "@/components/console/ApplicationSubmitted"
import {ApplyForm} from "@/components/console/ApplyForm"
import {statusOf} from "@/lib/api/client"
import {consoleKeys, getMerchantApplication} from "@/lib/api/console"
import {useMerchantSelection} from "@/lib/console/selection"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** /console/apply: the form, or the pending application it already sent. */
export default function ApplyPage() {
  const {t, i18n} = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const {scope, organization} = useMerchantSelection()
  useDocumentTitle(t("console.apply.title"))
  const live = organization.merchant_status === "live" || organization.merchant_status === "suspended"

  useEffect(() => {
    if (live) router.replace("/console/overview")
  }, [live, router])

  const {data: application, isPending, error, refetch} = useQuery({
    queryKey: consoleKeys.merchantApplication(scope),
    queryFn: () => getMerchantApplication(scope),
    enabled: !live,
  })

  if (live || isPending) return <Skeleton className="h-40 max-w-xl"/>
  if (error) {
    return (
      <ErrorState
        status={statusOf(error) === 503 ? 503 : 500}
        locale={i18n.language}
        action={<Button variant="outline" onClick={() => void refetch()}>{t("console.apply.retry")}</Button>}
      />
    )
  }
  if (application?.status === "pending") return <ApplicationSubmitted application={application}/>

  return (
    <div className="space-y-6">
      <PageHeader title={t("console.apply.title")} description={t("console.apply.description", {name: organization.display_name})}/>
      <ApplyForm onApplied={app => {
        queryClient.setQueryData(consoleKeys.merchantApplication(scope), app)
        void queryClient.invalidateQueries({queryKey: consoleKeys.session()})
      }}/>
    </div>
  )
}

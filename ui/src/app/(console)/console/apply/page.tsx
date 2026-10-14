"use client";

import {Button, ErrorState, PageHeader, Skeleton} from "@aoctech/ui";
import {useQuery, useQueryClient} from "@tanstack/react-query";
import {useRouter} from "next/navigation";
import {useEffect} from "react";
import {useTranslation} from "react-i18next";

import {ApplicationSubmitted} from "@/components/console/ApplicationSubmitted";
import {ApplySteps} from "@/components/console/ApplySteps";
import {ApplyForm} from "@/components/console/ApplyForm";
import {statusOf} from "@/lib/api/client";
import {consoleKeys, getMerchantApplication} from "@/lib/api/console";
import {useMerchantSelection} from "@/lib/console/selection";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/** /console/apply: the form, or the pending application it already sent. */
export default function ApplyPage() {
  const {t, i18n} = useTranslation();
  const router = useRouter();
  const queryClient = useQueryClient();
  const {scope, organization} = useMerchantSelection();
  useDocumentTitle(t("console.apply.title"));
  const live = organization.merchant_status === "live" || organization.merchant_status === "suspended";

  useEffect(() => {
    if (live) router.replace("/console/overview");
  }, [live, router]);

  const {data: application, isPending, error, refetch} = useQuery({
    queryKey: consoleKeys.merchantApplication(scope),
    queryFn: () => getMerchantApplication(scope),
    enabled: !live,
  });

  if (live || isPending) return <Skeleton className="h-40 max-w-xl"/>;
  if (error) {
    return (
      <ErrorState
        status={statusOf(error) === 503 ? 503 : 500}
        locale={i18n.language}
        action={<Button variant="outline" onClick={() => void refetch()}>{t("console.apply.retry")}</Button>}
      />
    );
  }
  if (application?.status === "pending") return <ApplicationSubmitted application={application}/>;

  return (
    <div className="max-w-5xl space-y-6">
      <PageHeader title={t("console.apply.title")} description={t("console.apply.description", {name: organization.display_name})}/>
      {/* The form, and beside it from lg what happens once it is sent. */}
      <div className="grid gap-10 lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)] lg:gap-16">
        <ApplyForm onApplied={app => {
          queryClient.setQueryData(consoleKeys.merchantApplication(scope), app);
          void queryClient.invalidateQueries({queryKey: consoleKeys.session()});
        }}/>
        <ApplySteps className="lg:pt-1"/>
      </div>
    </div>
  );
}

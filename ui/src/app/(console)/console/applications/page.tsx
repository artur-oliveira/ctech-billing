"use client";

import {Button, EmptyState, ErrorState, PageHeader, Segmented, Skeleton} from "@aoctech/ui";
import {useInfiniteQuery} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {ApplicationCard} from "@/components/console/ApplicationCard";
import {statusOf} from "@/lib/api/client";
import {consoleKeys, listApplications} from "@/lib/api/console";
import type {ApplicationStatus} from "@/lib/api/consoleTypes";
import {useConsoleScope} from "@/lib/console/scope";
import {NARROW, useMediaQuery} from "@/lib/hooks/useMediaQuery";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

const STATUSES: ApplicationStatus[] = ["pending", "approved", "rejected"];

/** Solicitações: tenant zero only; any other tenant gets the server's 404. */
export default function ApplicationsPage() {
  const {t, i18n} = useTranslation();
  const scope = useConsoleScope();
  useDocumentTitle(t("console.applications.title"));
  const phone = useMediaQuery(NARROW);
  const [status, setStatus] = useState<ApplicationStatus>("pending");
  const q = useInfiniteQuery({
    queryKey: consoleKeys.applications(scope, status),
    queryFn: ({pageParam}) => listApplications(scope, status, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
  });
  const items = q.data?.pages.flatMap(p => p.data) ?? [];

  return (
    <div className="space-y-6">
      <PageHeader title={t("console.applications.title")} description={t("console.applications.description")}/>
      <Segmented
        label={t("console.applications.filter")}
        value={status}
        onValueChange={setStatus}
        fill={phone}
        options={STATUSES.map(s => ({value: s, label: t(`console.applications.status.${s}`)}))}
      />
      {q.isPending ? (
        <div className="space-y-3" aria-busy><Skeleton className="h-36 w-full"/><Skeleton className="h-36 w-full"/></div>
      ) : q.isError ? (
        <ErrorState
          status={statusOf(q.error) === 404 ? 404 : 500}
          locale={i18n.language}
          action={statusOf(q.error) === 404 ? undefined : <Button variant="outline" onClick={() => void q.refetch()}>{t("console.apply.retry")}</Button>}
        />
      ) : items.length === 0 ? (
        <EmptyState title={t(`console.applications.empty.${status}`)} className="max-sm:items-start max-sm:text-left"/>
      ) : (
        <div>
          {items.map(a => <ApplicationCard key={a.id} application={a}/>)}
          {q.hasNextPage && (
            <Button variant="outline" className="mt-5 max-sm:w-full" disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
              {t("console.applications.more")}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

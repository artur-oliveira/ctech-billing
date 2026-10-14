"use client";

import {buttonVariants, cn, PageHeader, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {Landmark} from "lucide-react";
import Link from "next/link";
import {useTranslation} from "react-i18next";

import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {connectionKeys, listConnections} from "@/lib/api/connections";
import type {ConnectionsScope} from "@/lib/api/connectionsTypes";

import {ConnectionStatusBadge} from "./ConnectionHealth";

/**
 * The banks this organization may connect, with each one's state (BYOK spec
 * § 11). Only Inter in phase 1; a ruled list, one row per provider, the whole
 * row a link on a phone.
 */
export function ConnectionsList({scope, base}: {scope: ConnectionsScope; base: string}) {
  const {t} = useTranslation();
  const q = useQuery({queryKey: connectionKeys.list(scope), queryFn: () => listConnections(scope)});
  const providers = q.data?.providers ?? [];
  return (
    <div className="space-y-8">
      <PageHeader title={t("connections.title")} description={t("connections.intro")}/>
      {q.isPending ? (
        <div className="space-y-3" aria-busy><Skeleton className="h-12 w-full"/></div>
      ) : q.isError ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : (
        <ul className="divide-y divide-border border-y border-border">
          {providers.map(p => {
            const c = q.data?.data.find(x => x.provider === p.provider);
            const connected = c && c.state !== "onboarding";
            return (
              <li key={p.provider}>
                <Link href={`${base}/${p.provider}`} className="group grid grid-cols-[2.5rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2 py-4 sm:grid-cols-[2.5rem_minmax(0,1fr)_auto_auto]">
                  <span aria-hidden className="flex size-10 items-center justify-center rounded-lg border border-border bg-surface text-brand-600"><Landmark className="size-5"/></span>
                  <span className="min-w-0">
                    <span className="block font-medium text-foreground group-hover:underline">{t(`connections.providers.${p.provider}`)}</span>
                    <span className="block text-pretty text-sm text-muted-foreground">{t(`connections.providerBlurb.${p.provider}`)}</span>
                  </span>
                  <span className="col-start-2 sm:col-start-auto">
                    {c ? <ConnectionStatusBadge status={c.status}/> : <span className="text-sm text-muted-foreground">{t("connections.status.notConnected")}</span>}
                  </span>
                  <span className={cn(buttonVariants({variant: connected ? "outline" : "brand", size: "sm"}), "max-sm:hidden")}>
                    {connected ? t("connections.open") : t("connections.connect")}
                  </span>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

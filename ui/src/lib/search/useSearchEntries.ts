"use client";

import type {CommandEntry} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {useRouter} from "next/navigation";
import {useTranslation} from "react-i18next";

import {isNoBillingAccount} from "@/lib/api/client";
import {consoleKeys, getConsoleSession} from "@/lib/api/console";
import {getSession, portalKeys} from "@/lib/api/portal";
import {useAuth} from "@/lib/auth/AuthContext";
import {pickMerchantOrg, tenantInMode} from "@/lib/console/merchant";
import {useMerchantOrg} from "@/lib/console/useMerchantOrg";
import {useMode} from "@/lib/console/useMode";
import {type CreateKind, requestCreate} from "@/lib/createRequest";
import {useSpaceVerbs} from "@/lib/finance/useFinanceSpaces";
import {buildSearchEntries, type View} from "@/lib/search/entries";

/**
 * What this person can reach, from the queries the account menu already runs
 * (same keys, so no extra request). A subproduct whose access is still unknown
 * contributes nothing, except the one on screen, which is known by being there.
 */
export function useSearchEntries(view: View): CommandEntry[] {
  const {t} = useTranslation();
  const router = useRouter();
  const {authenticated} = useAuth();
  const mode = useMode();
  const stored = useMerchantOrg();
  const {personal, can} = useSpaceVerbs();

  const portalSession = useQuery({
    queryKey: portalKeys.session,
    queryFn: getSession,
    enabled: authenticated,
    retry: false,
    // See AccountMenu: re-asking a 403 on mount would loop the portal's empty state.
    retryOnMount: false,
    refetchOnWindowFocus: query => !isNoBillingAccount(query.state.error),
  });
  const consoleSession = useQuery({
    queryKey: consoleKeys.session(),
    queryFn: getConsoleSession,
    enabled: authenticated,
    retry: false,
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });

  const orgs = consoleSession.data?.organizations ?? [];
  const selected = orgs.length > 0 ? pickMerchantOrg(orgs, stored, mode) : null;

  const go = (href: string, kind?: CreateKind) => {
    // The screen takes the request when it mounts (or at once, if already there).
    if (kind) requestCreate(kind, href);
    router.push(href);
  };
  return buildSearchEntries(
    {
      portal: view === "portal" ? !isNoBillingAccount(portalSession.error) : portalSession.isSuccess,
      console: selected ? {
        active: tenantInMode(selected.merchant_status, mode),
        reviewer: selected.can_review_applications === true,
        addsRefused: mode === "live" && selected.merchant_status === "suspended",
      } : null,
      finance: {personal, canConfigure: can("finance.configure"), canWrite: can("finance.write")},
    },
    key => t(key),
    go,
    view,
  );
}

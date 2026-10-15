"use client";

import {BottomNavSpacer, SidebarNav, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {usePathname, useRouter} from "next/navigation";
import {useEffect} from "react";
import {useTranslation} from "react-i18next";

import {NoBillingAccount} from "@/components/portal/NoBillingAccount";
import {PortalSpaceScope} from "@/components/portal/PortalSpaceScope";
import {PortalSpaceSwitch} from "@/components/portal/PortalSpaceSwitch";
import {PortalBottomNav} from "@/components/portal/PortalBottomNav";
import {BillingShell} from "@/components/shell/BillingShell";
import {renderLink} from "@/components/shell/renderLink";
import {TermsGate} from "@/components/portal/TermsGate";
import {isNoBillingAccount} from "@/lib/api/client";
import {getSession, portalKeys} from "@/lib/api/portal";
import {useAuth} from "@/lib/auth/AuthContext";
import {useSubject} from "@/lib/auth/subject";
import {PORTAL_NAV} from "@/lib/portal/nav";
import {usePortalSpace} from "@/lib/portal/space";
import {RealtimeProvider} from "@/lib/realtime/RealtimeProvider";

/**
 * The consumer shell, and the gate in front of it.
 *
 * The page is `comfortable` (every @aoctech/ui control sizes itself for a
 * thumb); the frame's column is compact, the same as the console's, so the
 * three areas share one shell. The page takes the width of the area; each
 * screen chooses its own reading measure.
 *
 * The guard waits for `loading` before deciding. Skipping that check sends a
 * signed-in reader to /login on every hard refresh, because the boot-time
 * silent refresh has not answered yet when the first render happens. It is a
 * client-side guard and it is not the security boundary — the API rejects an
 * unauthenticated request whatever this renders. It exists so somebody whose
 * session ended sees a login instead of four failed panels.
 *
 * The tenant is never on this screen: the reader is a customer of CTech, and
 * the tenant is resolved from their session server-side. What can be on it is
 * whose bills these are — the person's own, or an organization they own or
 * administer (ADR 0025) — and only for someone who has such an organization.
 */
export default function PortalLayout({children}: LayoutProps<"/">) {
  const {t} = useTranslation();
  const router = useRouter();
  const {authenticated, loading} = useAuth();
  const subject = useSubject();
  // Read so the shell re-renders on a change of selection: the session key
  // below carries the space (portalKeys), and so must this read of it.
  usePortalSpace();

  const {data: session, error: sessionError} = useQuery({
    queryKey: portalKeys.session,
    queryFn: getSession,
    enabled: authenticated,
    // "No billing account" is an answer, not a hiccup: re-asking on focus
    // would clear it and flash the screens behind it before the 403 returns.
    refetchOnWindowFocus: query => !isNoBillingAccount(query.state.error),
  });

  // Signed in, nothing bought yet. Decided from the session alone rather than
  // per screen: every route below answers the same 403, so the alternative is
  // the same message three times under a nav whose every tab leads back to it.
  const noAccount = isNoBillingAccount(sessionError);

  useEffect(() => {
    if (!loading && !authenticated) router.replace("/login");
  }, [loading, authenticated, router]);

  const pathname = usePathname();
  const active = (href: string) => href === "/dashboard" ? pathname === href : pathname.startsWith(href);

  return (
    // The user scope only: the first frame carries the bearer and no space, so
    // the socket hears the signed-in person's own invoices and subscriptions.
    <RealtimeProvider surface={{kind: "portal"}} principal={authenticated && !noAccount ? subject : null}>
    <BillingShell
      view="portal"
      home="/dashboard"
      density="comfortable"
      nav={!noAccount && <SidebarNav label={t("portal.nav.label")} renderLink={renderLink}
        groups={[{items: PORTAL_NAV.map(item => ({href: item.href, label: t(item.labelKey), active: active(item.href)}))}]}/>}
      // Whose bills, for a person who also runs an organization; renders
      // nothing for everybody else. Stays with no billing account, since an
      // admin with nothing of their own still has the organization's.
      context={<PortalSpaceSwitch/>}
      bottomNav={<PortalBottomNav hidden={noAccount}/>}
    >
      {/* Four states, in this order. The empty account comes before the terms
          gate because there is no session to read `terms_accepted` from when
          it applies, and the gate comes before the children because it needs
          the session to have answered — rendering it on `undefined` would ask
          somebody who already agreed to agree again on every refresh. */}
      <PortalSpaceScope>
        {loading || !authenticated ? (
          <GateSkeleton/>
        ) : noAccount ? (
          <NoBillingAccount/>
        ) : session && !session.terms_accepted ? (
          <TermsGate/>
        ) : (
          children
        )}
      </PortalSpaceScope>
      {!noAccount && <BottomNavSpacer/>}
    </BillingShell>
    </RealtimeProvider>
  );
}

/** Held while the silent refresh answers. Not a spinner and not the word
 *  "carregando": the shell is already on screen, and what comes next is
 *  content in this shape. */
function GateSkeleton() {
  return (
    <div className="space-y-6" aria-busy>
      <Skeleton className="h-4 w-52"/>
      <Skeleton className="h-9 w-48"/>
      <Skeleton className="h-4 w-64"/>
    </div>
  );
}

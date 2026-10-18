"use client";

import {BottomNavSpacer, Button, ErrorState, SidebarNav, Skeleton, type SidebarNavGroup} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {usePathname, useRouter} from "next/navigation";
import {useEffect} from "react";
import {useTranslation} from "react-i18next";

import {BillingShell, ShellContext, TestBanner} from "@/components/shell/BillingShell";
import {renderNavLink} from "@/components/shell/renderLink";
import {MerchantInvitation} from "@/components/console/MerchantInvitation";
import {MerchantStatusBand} from "@/components/console/MerchantStatusBand";
import {OrganizationSwitch} from "@/components/console/OrganizationSwitch";
import {ConsoleBottomNav} from "@/components/console/ConsoleBottomNav";
import {statusOf} from "@/lib/api/client";
import {consoleKeys, getConsoleSession} from "@/lib/api/console";
import {useAuth} from "@/lib/auth/AuthContext";
import {useSubject} from "@/lib/auth/subject";
import {pickMerchantOrg, setMerchantOrg, tenantInMode} from "@/lib/console/merchant";
import {consoleNav} from "@/lib/console/nav";
import {ConsoleScopeProvider} from "@/lib/console/scope";
import {MerchantSelectionProvider} from "@/lib/console/selection";
import {useMerchantOrg} from "@/lib/console/useMerchantOrg";
import {useMode} from "@/lib/console/useMode";
import {FINANCE_HREF} from "@/lib/finance/href";
import {RealtimeProvider} from "@/lib/realtime/RealtimeProvider";

/**
 * The operator shell — invoicing, the second of the three areas the app ships
 * (portal, console, Finanças), and the same components as the first at a
 * different density.
 *
 * `<DensityScope density="compact">` is the whole of that difference: every
 * control from `@aoctech/ui` reads its `data-density` and sizes itself to 32px
 * instead of 44, and the drawers, modals, selects and menus opened from here
 * (portaled, outside this element) follow the scope too, on a desktop as well
 * (UX batch 5; under touch @aoctech/ui's touch.css draws them 36px with a 44px
 * target). An operator
 * works in this all day and wants rows on screen; the same person opens the
 * portal twice a month on a phone and wants a thumb target. Neither is a prop
 * a call site remembers to pass.
 *
 * Wider than the portal, too: `max-w-6xl` against `max-w-2xl`. The portal reads
 * one bill; this reads a table.
 *
 * The mode is in the header and never anywhere else. It is the one thing an
 * operator switches that changes which world the numbers come from, and acting
 * on the wrong one is the expensive mistake. The organization is switchable: it
 * is the selected account organization where the person is owner or admin
 * (ADR 0028), a request the server re-authorizes on every call; the mode is the
 * other switch.
 */
export default function ConsoleLayout({children}: LayoutProps<"/console">) {
  const {t, i18n} = useTranslation();
  const pathname = usePathname();
  const router = useRouter();
  const {authenticated, loading} = useAuth();
  const mode = useMode();
  const subject = useSubject();
  const stored = useMerchantOrg();
  const {data: session, error, isPending, refetch} = useQuery({
    queryKey: consoleKeys.session(),
    queryFn: getConsoleSession,
    enabled: authenticated,
    retry: false,
    staleTime: 60_000,
  });

  const organizations = session?.organizations ?? [];
  const unavailable = session?.organizations_unavailable ?? false;
  const selected = session ? pickMerchantOrg(organizations, stored, mode) : null;
  const active = selected !== null && tenantInMode(selected.merchant_status, mode);
  const org = selected?.organization_id;
  // Stable per (mode, organization): the React Compiler memoizes it.
  const scope = org ? {mode, org} : null;
  // The selected organization with or without a tenant in this mode: the
  // invitation, the apply form and the status band read it.
  const selection = selected && scope ? {scope, organization: selected} : null;
  const onApply = pathname.startsWith("/console/apply");
  // Bank connections need no tenant (BYOK spec § 3.1): an organization may
  // connect for its statement before it bills through CTech.
  const onConnections = pathname.startsWith("/console/connections");
  const reviewer = selected?.can_review_applications === true;

  // Owner or admin of no organization: nothing here is theirs; Finanças is.
  const noOrganization = statusOf(error) === 403 || (session !== undefined && !unavailable && organizations.length === 0);

  // A stored organization the person no longer administers is replaced once.
  useEffect(() => {
    if (org && org !== stored) setMerchantOrg(org);
  }, [org, stored]);

  useEffect(() => {
    if (!loading && !authenticated) router.replace("/login");
  }, [loading, authenticated, router]);

  useEffect(() => {
    if (noOrganization) router.replace(FINANCE_HREF);
  }, [noOrganization, router]);

  const nav = consoleNav(active, reviewer);
  const isCurrent = (href: string) => href === "/console/overview" ? pathname === href : pathname.startsWith(href);
  const groups: SidebarNavGroup[] = NAV_GROUPS
    .map(group => ({
      label: group.label && t(group.label),
      items: group.items.filter(href => nav.some(item => item.href === href)).map(href => ({
        href, label: t(nav.find(item => item.href === href)!.label), active: isCurrent(href),
      })),
    }))
    .filter(group => group.items.length > 0);

  return (
    <BillingShell
      view="console"
      home="/console/overview"
      density="compact"
      nav={groups.length > 0 && <SidebarNav label={t("console.nav.label")} groups={groups} renderLink={renderNavLink}/>}
      // Whose billing, and the test mark beside it; the mode is switched in
      // the account menu and announced by the band above the page.
      context={selected && (
        <ShellContext test={mode === "test"} testLabel={t("console.mode.test")}>
          <OrganizationSwitch organizations={organizations} selected={selected}/>
        </ShellContext>
      )}
      banner={<>
        {/* Deliberately the loudest thing on the page after the danger colour:
            every number below it is fake, and an operator who forgets that
            voids a real invoice believing it is a sandbox one. */}
        {mode === "test" && <TestBanner>{t("console.shell.testBanner")}</TestBanner>}
        {selection && !onApply && (
          <MerchantSelectionProvider value={selection}><MerchantStatusBand/></MerchantSelectionProvider>
        )}
      </>}
      bottomNav={active && <ConsoleBottomNav hasOrganization={active} reviewer={reviewer}/>}
    >
      {loading || !authenticated || noOrganization || (session === undefined && isPending) ? (
        <ShellSkeleton/>
      ) : unavailable || (error && !session) ? (
        // ctech-account did not answer, or the session itself failed: the
        // list is unknown, not empty, so nothing is chosen and nobody is
        // sent away. One retry, in the console's words.
        <ErrorState
          status={unavailable ? 503 : 500}
          locale={i18n.language}
          description={t("console.organization.unavailable")}
          action={<Button variant="outline" onClick={() => void refetch()}>{t("console.organization.retry")}</Button>}
        />
      ) : selection ? (
        // No tenant in this mode is a beginning: the invitation, or where
        // the application stands. /console/apply runs without a tenant.
        <MerchantSelectionProvider value={selection}>
          {onApply ? children : onConnections || active ? (
            <ConsoleScopeProvider value={selection.scope}>
              {/* The organization and mode are the connection's key: switching either one closes the socket and opens another with no cursor. */}
              <RealtimeProvider surface={{kind: "console", scope: selection.scope}} principal={subject}>
                {children}
              </RealtimeProvider>
            </ConsoleScopeProvider>
          ) : <MerchantInvitation/>}
        </MerchantSelectionProvider>
      ) : (
        <ShellSkeleton/>
      )}
      {active && <BottomNavSpacer/>}
    </BillingShell>
  );
}

/** The sections in the column, grouped by what an operator is doing. */
const NAV_GROUPS: {label?: string; items: string[]}[] = [
  {items: ["/console/overview", "/console/invoices", "/console/subscriptions", "/console/customers", "/console/catalog"]},
  {label: "console.nav.groupCollection", items: ["/console/connections", "/console/plan"]},
  {label: "console.nav.groupIntegrations", items: ["/console/credentials", "/console/webhooks"]},
  {label: "console.nav.groupOrganization", items: ["/console/settings", "/console/applications"]},
];


function ShellSkeleton() {
  return (
    <div className="space-y-4" aria-busy>
      <Skeleton className="h-6 w-40"/>
      <Skeleton className="h-4 w-full"/>
      <Skeleton className="h-4 w-5/6"/>
    </div>
  );
}

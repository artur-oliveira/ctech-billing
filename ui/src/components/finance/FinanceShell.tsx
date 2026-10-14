"use client";

import {BottomNavSpacer, Skeleton} from "@aoctech/ui";
import {useRouter} from "next/navigation";
import {type ReactNode, useEffect} from "react";
import {useTranslation} from "react-i18next";

import {FinanceBottomNav} from "@/components/finance/FinanceBottomNav";
import {SpaceFinanceNav} from "@/components/finance/FinanceNav";
import {BillingShell, ShellContext, TestBanner} from "@/components/shell/BillingShell";
import {SpaceSwitch} from "@/components/finance/SpaceSwitch";
import {useAuth} from "@/lib/auth/AuthContext";
import {useMode} from "@/lib/console/useMode";
import {FINANCE_HREF} from "@/lib/finance/href";

/**
 * Finanças, the third area beside the portal and the console. It was a section
 * of the console; it is its own product now, because most people who use it
 * have no organization and nothing else in the console is theirs.
 *
 * The console's density and frame (a person keeps their books in tables): the
 * column holds the space with the test mark beside it, Finanças' own sections
 * and the person; on a phone the sections are the bar at the bottom.
 *
 * No session probe: everybody signed in has a personal space (ADR 0025), so
 * there is no "not yours" state to decide before rendering.
 */
export function FinanceShell({children}: {children: ReactNode}) {
  const {t} = useTranslation();
  const router = useRouter();
  const {authenticated, loading} = useAuth();
  const mode = useMode();

  useEffect(() => {
    if (!loading && !authenticated) router.replace("/login");
  }, [loading, authenticated, router]);

  return (
    <BillingShell
      view="finance"
      home={FINANCE_HREF}
      density="compact"
      nav={<SpaceFinanceNav/>}
      context={<ShellContext test={mode === "test"} testLabel={t("console.mode.test")}><SpaceSwitch/></ShellContext>}
      banner={mode === "test" && <TestBanner>{t("finance.shell.testBanner")}</TestBanner>}
      bottomNav={!loading && authenticated && <FinanceBottomNav/>}
    >
      {loading || !authenticated ? (
        <div className="space-y-4" aria-busy>
          <Skeleton className="h-6 w-40"/>
          <Skeleton className="h-4 w-full"/>
          <Skeleton className="h-4 w-5/6"/>
        </div>
      ) : (
        <>
          {children}
          <BottomNavSpacer/>
        </>
      )}
    </BillingShell>
  );
}

import {toast} from "sonner";

import {messageFor, problemCode} from "@/lib/api/client";
import {t} from "@/lib/i18n";

/** The refusals a plan change fixes (BYOK part C). */
const PLAN_LIMITS = new Set(["byok_quota_exceeded", "subscription_limit_reached", "bank_connection_limit_reached"]);

/** toast.error, with "Ver planos" when a plan limit refused the action. */
export function toastError(error: unknown, open: (href: string) => void) {
  const action = PLAN_LIMITS.has(problemCode(error) ?? "")
    ? {action: {label: t("console.plan.see"), onClick: () => open("/console/plan")}}
    : undefined;
  toast.error(messageFor(error), action);
}

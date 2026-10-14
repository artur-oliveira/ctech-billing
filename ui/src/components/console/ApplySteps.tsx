"use client";

import {useTranslation} from "react-i18next";

const STEPS = ["send", "test", "live"] as const;

/**
 * What happens after "Quero cobrar meus clientes", in order: the order is the
 * information (test opens at once, live after the review), so the steps are
 * numbered. A ruled list, not cards.
 */
export function ApplySteps({className = ""}: {className?: string}) {
  const {t} = useTranslation();
  return (
    <section aria-labelledby="apply-steps" className={className}>
      <h2 id="apply-steps" className="text-sm font-medium text-muted-foreground">{t("console.invitation.how")}</h2>
      <ol className="mt-3 divide-y divide-border border-y border-border">
        {STEPS.map((step, i) => (
          <li key={step} className="flex gap-4 py-4">
            <span aria-hidden className="flex size-6 shrink-0 items-center justify-center rounded-full border border-border text-xs font-medium text-muted-foreground tabular-nums">
              {i + 1}
            </span>
            <div className="min-w-0 space-y-0.5">
              <p className="text-sm font-medium text-foreground">{t(`console.invitation.steps.${step}.title`)}</p>
              <p className="text-sm text-muted-foreground">{t(`console.invitation.steps.${step}.body`)}</p>
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
}

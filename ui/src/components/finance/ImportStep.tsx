import {useId} from "react";

/**
 * One numbered step of an import flow (file, then review): the number, a heading
 * that names the landmark, an optional one-line hint, then the step's content at
 * one rhythm. Shared by the account import and the card-statement import so the
 * two read as one flow.
 */
export function ImportStep({n, title, hint, children}: {n: number; title: string; hint?: string; children: React.ReactNode}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="space-y-3">
      <div className="flex items-start gap-3">
        <span aria-hidden className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-full bg-surface text-xs font-semibold tabular-nums text-muted-foreground">{n}</span>
        <div className="min-w-0">
          <h2 id={id} className="text-sm font-medium text-foreground">{title}</h2>
          {hint && <p className="text-sm text-muted-foreground">{hint}</p>}
        </div>
      </div>
      <div className="space-y-3">{children}</div>
    </section>
  );
}

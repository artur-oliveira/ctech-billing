import {useId} from "react";

interface SectionProps {
  title: string
  /** A secondary control on the heading's row: a "see all" link, a total. */
  action?: React.ReactNode
  /** The title is already on screen (a tab, a label above): keep it for the landmark, not the eye. */
  hideTitle?: boolean
  /** The heading's ink: `foreground` inside Finanças, `muted` for the portal and console's quiet group labels. */
  tone?: "foreground" | "muted"
  className?: string
  children: React.ReactNode
}

/**
 * A titled block of a screen: a landmark named by its own h2 (id from useId, so
 * two blocks never collide and a title never has to be slugged), the heading
 * row with an optional action, then the content at one vertical rhythm.
 */
export function Section({title, action, hideTitle, tone = "foreground", className = "space-y-3", children}: SectionProps) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={className}>
      <div className={hideTitle && !action ? undefined : `flex flex-wrap items-center gap-x-3 gap-y-1 ${hideTitle ? "justify-end" : "justify-between"}`}>
        <h2 id={id} className={hideTitle ? "sr-only" : `text-sm font-medium ${tone === "muted" ? "text-muted-foreground" : "text-foreground"}`}>{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}

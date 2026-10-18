const TONE = {
  default: "",
  foreground: "text-foreground",
  muted: "text-muted-foreground",
  danger: "text-danger",
  success: "text-success",
} as const;

interface AmountProps {
  tone?: keyof typeof TONE
  /** Medium weight: a total or a headline figure. */
  strong?: boolean
  /** A reversed or skipped amount: struck through, muted. */
  struck?: boolean
  className?: string
  children: React.ReactNode
}

/**
 * A formatted amount, drawn with tabular figures (`data-numeric`, globals.css)
 * so a column of them lines up digit under digit. The caller formats (`money`,
 * `signedMoney`); this only owns how a figure looks.
 */
export function Amount({tone = "default", strong, struck, className = "", children}: AmountProps) {
  const cls = [TONE[struck ? "muted" : tone], strong && "font-medium", struck && "line-through", className].filter(Boolean).join(" ");
  return <span data-numeric className={cls || undefined}>{children}</span>;
}

interface AmountRowProps {
  label: React.ReactNode
  /** The figure, usually an `<Amount>`. */
  children: React.ReactNode
  /** A total: the whole row in medium weight. */
  strong?: boolean
  /** `li` inside a `ul`; the default is a `dt`/`dd` pair inside a `dl`. */
  as?: "div" | "li"
}

/** A label and its figure on one line, the figure pinned right and never shrunk: a ledger line, a balance, a total. */
export function AmountRow({label, children, strong, as = "div"}: AmountRowProps) {
  const cls = `flex items-center justify-between gap-3 py-2${strong ? " font-medium" : ""}`;
  const left = "min-w-0 truncate";
  const right = "shrink-0";
  return as === "li"
    ? <li className={cls}><span className={left}>{label}</span><span className={right}>{children}</span></li>
    : <div className={cls}><dt className={left}>{label}</dt><dd className={right}>{children}</dd></div>;
}

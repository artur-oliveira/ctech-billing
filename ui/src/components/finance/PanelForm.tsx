import type {ComponentProps} from "react";

/**
 * The form that opens in place under a row (settle, edit): a tinted card, two
 * columns from `sm`. `items-start` because each Field is itself a grid: stretched
 * to the row's height, the spare space would go to its label row and push the
 * input out of line with its neighbour's.
 */
export const IN_PANEL = "mt-3 grid items-start gap-3 rounded-lg bg-surface p-3 motion-safe:animate-in motion-safe:fade-in [&>*]:min-w-0";

export function PanelForm({columns = "sm:grid-cols-2", className = "", ...props}: ComponentProps<"form"> & {
  /** The grid template from `sm`, for a form that is not two equal columns. */
  columns?: string
}) {
  return <form className={`${IN_PANEL} ${columns} ${className}`.trim()} {...props}/>;
}

/**
 * The form inside a PanelDrawer (new card, purchase, transfer): the drawer is
 * already the container, so no tint, only the same grid and the same
 * `items-start`/`min-w-0` rules as PanelForm.
 */
export function DrawerForm({columns = "sm:grid-cols-2", className = "", ...props}: ComponentProps<"form"> & {columns?: string}) {
  return <form className={`grid items-start gap-x-4 gap-y-3 ${columns} ${className} [&>*]:min-w-0`.replace(/\s+/g, " ")} {...props}/>;
}

/** The buttons (and the error) row of a PanelForm or DrawerForm: the full width, wrapping. */
export function PanelActions({children}: {children: React.ReactNode}) {
  return <div className="flex flex-wrap items-center gap-2 sm:col-span-full">{children}</div>;
}

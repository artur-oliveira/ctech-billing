import type {RenderLink} from "@aoctech/ui";
import Link from "next/link";

export const renderLink: RenderLink = props => <Link {...props}/>;

/**
 * A section rail's link. The rail draws 32px rows for a mouse (compact density);
 * under a finger every row is a 44px target. `!` because the lib's own
 * density utility on the row is as specific as `touch:`.
 */
export const renderNavLink: RenderLink = ({className, ...props}) => (
  <Link {...props} className={`${className ?? ""} touch:min-h-11!`}/>
);

export type ShellView = "portal" | "console" | "finance"

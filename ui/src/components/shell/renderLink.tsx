import type {RenderLink} from "@aoctech/ui"
import Link from "next/link"

export const renderLink: RenderLink = props => <Link {...props}/>

export type ShellView = "portal" | "console" | "finance"

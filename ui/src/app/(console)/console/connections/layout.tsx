import type {Metadata} from "next"

export const metadata: Metadata = {title: "Conexões · Console"}

export default function Layout({children}: LayoutProps<"/console/connections">) {
  return children
}

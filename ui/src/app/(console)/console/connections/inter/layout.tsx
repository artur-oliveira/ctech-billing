import type {Metadata} from "next"

export const metadata: Metadata = {title: "Banco Inter · Conexões · Console"}

export default function Layout({children}: LayoutProps<"/console/connections/inter">) {
  return children
}

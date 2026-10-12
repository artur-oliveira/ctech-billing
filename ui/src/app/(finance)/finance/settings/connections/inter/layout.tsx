import type {Metadata} from "next"

export const metadata: Metadata = {title: "Banco Inter · Conexões · Finanças"}

export default function Layout({children}: LayoutProps<"/finance/settings/connections/inter">) {
  return children
}

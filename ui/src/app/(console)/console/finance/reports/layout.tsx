import type {Metadata} from "next"

export const metadata: Metadata = {title: "Relatórios · Finanças"}

export default function Layout({children}: LayoutProps<"/console/finance/reports">) {
  return children
}

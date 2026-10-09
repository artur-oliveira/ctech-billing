import type {Metadata} from "next"

export const metadata: Metadata = {title: "Extrato"}

export default function Layout({children}: LayoutProps<"/console/finance/statement">) {
  return children
}

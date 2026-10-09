import type {Metadata} from "next"

export const metadata: Metadata = {title: "Extrato · Finanças"}

export default function Layout({children}: LayoutProps<"/console/finance/statement">) {
  return children
}

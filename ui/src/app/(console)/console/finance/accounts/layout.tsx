import type {Metadata} from "next"

export const metadata: Metadata = {title: "Contas · Finanças"}

export default function Layout({children}: LayoutProps<"/console/finance/accounts">) {
  return children
}

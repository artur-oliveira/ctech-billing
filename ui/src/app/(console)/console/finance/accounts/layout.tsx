import type {Metadata} from "next"

export const metadata: Metadata = {title: "Contas"}

export default function Layout({children}: LayoutProps<"/console/finance/accounts">) {
  return children
}

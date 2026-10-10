import type {Metadata} from "next"

export const metadata: Metadata = {title: "Contas"}

export default function Layout({children}: LayoutProps<"/finance/accounts">) {
  return children
}

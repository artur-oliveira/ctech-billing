import type {Metadata} from "next"

export const metadata: Metadata = {title: "Cartões"}

export default function Layout({children}: LayoutProps<"/finance/cards">) {
  return children
}

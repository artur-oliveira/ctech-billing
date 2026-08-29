import type {Metadata} from "next"

export const metadata: Metadata = {title: "Faturas · Console"}

export default function Layout({children}: LayoutProps<"/console/invoices">) {
  return children
}

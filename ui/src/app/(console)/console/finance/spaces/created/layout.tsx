import type {Metadata} from "next"

export const metadata: Metadata = {title: "Finanças"}

export default function Layout({children}: LayoutProps<"/console/finance/spaces/created">) {
  return children
}

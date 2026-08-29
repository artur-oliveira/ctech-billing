import type {Metadata} from "next"

export const metadata: Metadata = {title: "Assinatura · Console"}

export default function Layout({children}: LayoutProps<"/console/subscription">) {
  return children
}

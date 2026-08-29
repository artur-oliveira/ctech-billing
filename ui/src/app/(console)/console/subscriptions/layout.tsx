import type {Metadata} from "next"

export const metadata: Metadata = {title: "Assinaturas · Console"}

export default function Layout({children}: LayoutProps<"/console/subscriptions">) {
  return children
}

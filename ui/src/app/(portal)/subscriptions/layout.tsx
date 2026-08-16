import type {Metadata} from "next"

export const metadata: Metadata = {title: "Assinaturas"}

export default function Layout({children}: LayoutProps<"/subscriptions">) {
  return children
}

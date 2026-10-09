import type {Metadata} from "next"

export const metadata: Metadata = {title: "Início"}

export default function Layout({children}: LayoutProps<"/dashboard">) {
  return children
}

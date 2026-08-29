import type {Metadata} from "next"

export const metadata: Metadata = {title: "Visão geral · Console"}

export default function Layout({children}: LayoutProps<"/console/overview">) {
  return children
}

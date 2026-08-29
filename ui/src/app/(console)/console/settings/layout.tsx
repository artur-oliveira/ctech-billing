import type {Metadata} from "next"

export const metadata: Metadata = {title: "Configurações · Console"}

export default function Layout({children}: LayoutProps<"/console/settings">) {
  return children
}

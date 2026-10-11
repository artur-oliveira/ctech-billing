import type {Metadata} from "next"

export const metadata: Metadata = {title: "Credenciais · Console"}

export default function Layout({children}: LayoutProps<"/console/credentials">) {
  return children
}

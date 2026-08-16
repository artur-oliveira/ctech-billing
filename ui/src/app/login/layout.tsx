import type {Metadata} from "next"

export const metadata: Metadata = {title: "Entrar"}

export default function Layout({children}: LayoutProps<"/login">) {
  return children
}

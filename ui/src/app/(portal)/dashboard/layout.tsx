import type {Metadata} from "next"

export const metadata: Metadata = {title: "Suas cobranças"}

export default function Layout({children}: LayoutProps<"/dashboard">) {
  return children
}

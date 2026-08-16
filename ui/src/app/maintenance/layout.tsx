import type {Metadata} from "next"

export const metadata: Metadata = {title: "Em manutenção"}

export default function Layout({children}: LayoutProps<"/maintenance">) {
  return children
}

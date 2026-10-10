import type {Metadata} from "next"

export const metadata: Metadata = {title: "Recorrências"}

export default function Layout({children}: LayoutProps<"/finance/recurrences">) {
  return children
}

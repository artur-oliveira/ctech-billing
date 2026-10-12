import type {Metadata} from "next"

export const metadata: Metadata = {title: "A classificar"}

export default function Layout({children}: LayoutProps<"/finance/unclassified">) {
  return children
}

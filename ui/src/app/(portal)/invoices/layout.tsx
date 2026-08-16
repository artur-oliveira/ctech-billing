import type {Metadata} from "next"

// A layout that exists only to name the screen. The page itself is a client
// component — it holds the infinite query — and a client component cannot
// export metadata, so the title lives one level up. Same for /invoice and
// /subscriptions.
export const metadata: Metadata = {title: "Faturas"}

export default function Layout({children}: LayoutProps<"/invoices">) {
  return children
}

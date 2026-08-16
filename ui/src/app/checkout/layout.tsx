import type {Metadata} from "next"

/**
 * `noindex` is inherited from the root layout and must stay: a payment link in
 * a search result is a payment link in the wrong hands. The Open Graph tags,
 * also inherited, still apply — preview scrapers ignore robots, so a link
 * pasted into WhatsApp unfurls as CTech Billing rather than a bare URL.
 */
export const metadata: Metadata = {title: "Pagar fatura"}

export default function Layout({children}: LayoutProps<"/checkout">) {
  return children
}

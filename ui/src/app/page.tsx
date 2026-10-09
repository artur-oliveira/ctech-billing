import type {Metadata} from "next"

import {LandingContent} from "@/components/LandingContent"

/**
 * The one indexable route in this app, overriding the root layout's `noindex`.
 *
 * It is safe to index precisely because it carries no data: a name, a sentence
 * and a login button. Every route below it is one customer's billing history
 * and stays out of every crawler's reach.
 */
export const metadata: Metadata = {
  title: {absolute: "CTech Billing · faturas, assinaturas e finanças"},
  description:
    "Faturas, assinaturas e finanças.",
  robots: {index: true, follow: true},
  alternates: {canonical: "/"},
}

/** The public front door: a name, one line and a sign-in button. */
export default function Landing() {
  return <LandingContent/>
}

import type {Metadata, Viewport} from "next"
import {IBM_Plex_Sans} from "next/font/google"

import {Providers} from "@/app/providers"
import "./globals.css"

const sans = IBM_Plex_Sans({variable: "--font-sans", subsets: ["latin"], display: "swap"})

/** Where a relative OG image resolves from. Without it Next warns at build and
 *  every social preview points at localhost. */
const SITE_URL = process.env.NEXT_PUBLIC_SITE_URL || "https://billing.aoctech.app"

/**
 * Metadata for a portal that must not be indexed.
 *
 * `robots: noindex, nofollow` is the important line and it is not an oversight.
 * Every screen behind this layout is one customer's billing data; a search
 * result pointing at `/invoice?id=…` is a liability, not traffic. The public
 * marketing page, when it exists, is a different route with its own metadata
 * and is where SEO in the ranking sense belongs.
 *
 * What the tags here are actually for: the browser tab, the PWA install, and
 * the link preview. A checkout link gets pasted into WhatsApp, and what unfurls
 * there should be the CTech mark and a sentence — not a bare URL. Preview
 * scrapers read Open Graph and ignore robots, which is exactly the split we
 * want.
 */
export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: {default: "Billing · CTech", template: "%s · CTech Billing"},
  description: "Suas faturas e assinaturas na CTech. Pague com PIX em segundos.",
  applicationName: "CTech Billing",
  robots: {index: false, follow: false, nocache: true},
  manifest: "/site.webmanifest",
  icons: {
    icon: [
      {url: "/favicon-32x32.png", sizes: "32x32", type: "image/png"},
      {url: "/favicon-16x16.png", sizes: "16x16", type: "image/png"},
    ],
    apple: "/apple-touch-icon.png",
    shortcut: "/favicon.ico",
  },
  openGraph: {
    type: "website",
    siteName: "CTech Billing",
    locale: "pt_BR",
    title: "CTech Billing",
    description: "Suas faturas e assinaturas na CTech. Pague com PIX em segundos.",
    images: [{url: "/android-chrome-512x512.png", width: 512, height: 512, alt: "CTech"}],
  },
  twitter: {card: "summary", title: "CTech Billing"},
  formatDetection: {telephone: false, email: false, address: false},
}

export const viewport: Viewport = {
  // The brand, not white: this is the colour of the Android task-switcher bar
  // and the iOS status bar on an installed PWA, and a white one there makes the
  // app look like a browser tab that lost its chrome.
  themeColor: "#7c3f22",
  // The portal is read on a phone, at night, by someone who wants one answer.
  // Pinch-zoom stays available; nothing here is laid out on the assumption
  // that it will not be used.
  width: "device-width",
  initialScale: 1,
}

export default function RootLayout({children}: LayoutProps<"/">) {
  return (
    <html lang="pt-BR" className={`${sans.variable} h-full`} suppressHydrationWarning>
    <body className="min-h-full">
    <Providers>{children}</Providers>
    </body>
    </html>
  )
}

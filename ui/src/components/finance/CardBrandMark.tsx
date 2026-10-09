import type {CardBrand} from "@/lib/api/financeTypes"
import {t} from "@/lib/i18n"

/**
 * A card network's mark, small (a 32×20 card), for telling cards apart.
 *
 * Simplified marks drawn for this repository (no third-party asset, so no
 * licence beyond the project's own): the shapes and colours that make each
 * network recognisable at 20px, not reproductions of the official artwork. The
 * marks are trademarks of their owners, shown only to identify which card is
 * which (nominative use), never as an endorsement.
 *
 * Always decoration: the brand's name is written beside it wherever it appears,
 * so the SVG is aria-hidden.
 */
export const CARD_BRANDS: CardBrand[] = ["visa", "mastercard", "elo", "amex", "hipercard", "diners", "other"]

export const brandName = (b: CardBrand) => t(`finance.cards.brands.${b}`)

export function CardBrandMark({brand, className = ""}: {brand: CardBrand; className?: string}) {
  return (
    <svg data-brand={brand} aria-hidden viewBox="0 0 32 20" width="32" height="20" className={`shrink-0 ${className}`}>
      <Mark brand={brand}/>
    </svg>
  )
}

const label = {fontFamily: "IBM Plex Sans, system-ui, sans-serif", fontWeight: 700} as const

function Mark({brand}: {brand: CardBrand}) {
  switch (brand) {
    case "visa":
      return (
        <>
          <rect width="32" height="20" rx="3" fill="#1a1f71"/>
          <text x="16" y="13.6" textAnchor="middle" fontSize="9" fontStyle="italic" fill="#fff" style={label} letterSpacing="0.3">VISA</text>
          <rect x="5" y="15.5" width="22" height="1.2" rx="0.6" fill="#f7b600"/>
        </>
      )
    case "mastercard":
      return (
        <>
          <rect width="32" height="20" rx="3" fill="#1f1f1f"/>
          <circle cx="12.5" cy="10" r="6" fill="#eb001b"/>
          <circle cx="19.5" cy="10" r="6" fill="#f79e1b"/>
          <path d="M16 5.2a6 6 0 0 1 0 9.6a6 6 0 0 1 0-9.6z" fill="#ff5f00"/>
        </>
      )
    case "elo":
      return (
        <>
          <rect width="32" height="20" rx="3" fill="#111"/>
          <text x="18.5" y="13.8" textAnchor="middle" fontSize="10" fill="#fff" style={label}>elo</text>
          <circle cx="7" cy="6.5" r="1.7" fill="#ffcb05"/>
          <circle cx="7" cy="10.5" r="1.7" fill="#00a4e0"/>
          <circle cx="7" cy="14.5" r="1.7" fill="#ef4123"/>
        </>
      )
    case "amex":
      return (
        <>
          <rect width="32" height="20" rx="3" fill="#006fcf"/>
          <text x="16" y="13.3" textAnchor="middle" fontSize="7.6" fill="#fff" style={label} letterSpacing="0.2">AMEX</text>
        </>
      )
    case "hipercard":
      return (
        <>
          <rect width="32" height="20" rx="3" fill="#b3131b"/>
          <text x="16" y="13.2" textAnchor="middle" fontSize="7.2" fill="#fff" style={label}>hiper</text>
        </>
      )
    case "diners":
      return (
        <>
          <rect width="32" height="20" rx="3" fill="#fff" stroke="#c7ccd1" strokeWidth="1"/>
          <circle cx="16" cy="10" r="6.5" fill="#0079be"/>
          <path d="M13.6 6.3a4.3 4.3 0 0 0 0 7.4z M18.4 6.3a4.3 4.3 0 0 1 0 7.4z" fill="#fff"/>
        </>
      )
    default:
      return (
        <>
          <rect x="0.5" y="0.5" width="31" height="19" rx="2.5" fill="#fff" stroke="currentColor" strokeOpacity="0.45"/>
          <rect x="0.5" y="5" width="31" height="3" fill="currentColor" fillOpacity="0.45"/>
          <rect x="4" y="12.5" width="9" height="2" rx="1" fill="currentColor" fillOpacity="0.45"/>
        </>
      )
  }
}

/** "•••• 1234": the only part of a number billing ever shows. */
export const maskedLast4 = (last4: string) => `•••• ${last4}`

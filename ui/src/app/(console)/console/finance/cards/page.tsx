"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {useTranslation} from "react-i18next"

import {CardsView} from "@/components/finance/CardsView"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/** F5 — cards and their statements. `?card=` picks the card; none, the first. */
export default function FinanceCardsPage() {
  const {t} = useTranslation()
  useDocumentTitle(t("finance.nav.cards"))
  return (
    <Suspense>
      <Cards/>
    </Suspense>
  )
}

function Cards() {
  const card = useSearchParams().get("card") ?? ""
  return <CardsView key={card} card={card}/>
}

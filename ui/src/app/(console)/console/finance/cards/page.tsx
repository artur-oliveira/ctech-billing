"use client"

import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {CardsView} from "@/components/finance/CardsView"

/** F5 — cards and their statements. `?card=` picks the card; none, the first. */
export default function FinanceCardsPage() {
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

"use client"

import {Suspense} from "react"

import {SpaceCreatedReturn} from "@/components/finance/SpaceCreatedReturn"

/** Where ctech-account sends the browser back after "Novo espaço" (ADR 0027). */
export default function SpaceCreatedPage() {
  return (
    <Suspense>
      <SpaceCreatedReturn/>
    </Suspense>
  )
}

"use client"

import {Button} from "@aoctech/ui"
import {useEffect} from "react"

import {StatusScreen} from "@/components/StatusScreen"

/**
 * The boundary for anything a screen throws while rendering.
 *
 * Failed *requests* never reach here — those are query errors, and each screen
 * shows them in place with a retry so a broken subscriptions call does not
 * take the invoice down with it (see ErrorBlock). What lands here is a real
 * defect, and the only honest thing to say is that it is ours.
 *
 * The reader gets `reset` first because a re-render fixes a surprising share
 * of these, and a way out second. The digest is shown small: it is the only
 * thing they can quote to support, and the message itself is a stack trace in
 * development and a redacted string in production.
 */
export default function Error({
  error,
  reset,
}: {
  error: Error & {digest?: string}
  reset: () => void
}) {
  useEffect(() => {
    // ponytail: console only. Wire to whatever the family settles on for
    // browser error reporting; nothing in ctech-* collects it today.
    console.error(error)
  }, [error])

  return (
    <StatusScreen
      title="Algo quebrou desse lado"
      description="Não foi você e nada foi cobrado. Tente de novo — se continuar, fale com a gente e informe o código abaixo."
      action={
        <div className="flex flex-col items-center gap-3">
          <Button onClick={reset}>Tentar de novo</Button>
          {error.digest && (
            <code className="font-mono text-xs text-muted-foreground">{error.digest}</code>
          )}
        </div>
      }
    />
  )
}

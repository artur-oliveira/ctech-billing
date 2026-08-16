"use client"

import {useEffect, useRef, useState} from "react"

import {getAccessToken} from "@/lib/api/client"
import {USE_MOCK} from "@/lib/mockConfig"

export type StreamStatus = "connecting" | "waiting" | "paid" | "lost"

/**
 * Waits for the charge to settle, over Server-Sent Events.
 *
 * SSE rather than a WebSocket because the traffic is one-directional — the
 * server says "paid" once and closes — and rather than polling because a
 * thirty-minute PIX window at three-second intervals is six hundred requests
 * to learn one fact.
 *
 * It is `fetch` and not `EventSource` for one reason: `EventSource` cannot set
 * an Authorization header, and the alternative is putting a bearer token in a
 * query string, where it lands in every access log and proxy trace between
 * here and the instance.
 *
 * The server side re-reads the invoice rather than holding an in-process
 * signal, so this is correct at any instance count — see the comment on
 * invoiceEvents in api/internal/api/v1/portal_events.go. Publishing settlement
 * over Valkey would cut the reads, but it is an optimisation, not a fix.
 */
export function usePaymentStream(invoiceId: string, enabled: boolean): StreamStatus {
  const [status, setStatus] = useState<StreamStatus>("connecting")
  // Held in a ref so a re-render caused by the status change does not tear
  // down and re-open the very stream that produced it.
  const idRef = useRef(invoiceId)
  idRef.current = invoiceId

  // Reset during render rather than in the effect. Setting it inside the effect
  // works but renders once with the previous invoice's status before correcting
  // itself — which for this hook means flashing "paid" onto a different bill.
  const key = `${invoiceId}|${enabled}`
  const [prevKey, setPrevKey] = useState(key)
  if (prevKey !== key) {
    setPrevKey(key)
    setStatus("connecting")
  }

  useEffect(() => {
    if (!enabled) return

    if (USE_MOCK) {
      let cancelled = false
      let timer: ReturnType<typeof setTimeout> | undefined
      void (async () => {
        const {settleAfterSeconds, settleInvoice} = await import("@/dev/mockRuntime")
        if (cancelled) return
        setStatus("waiting")
        const after = settleAfterSeconds()
        if (after === null) return
        timer = setTimeout(() => {
          settleInvoice(idRef.current)
          setStatus("paid")
        }, after * 1000)
      })()
      return () => {
        cancelled = true
        clearTimeout(timer)
      }
    }

    const abort = new AbortController()

    void (async () => {
      try {
        const token = getAccessToken()
        const response = await fetch(`/v1/portal/invoices/${invoiceId}/events`, {
          headers: {
            Accept: "text/event-stream",
            ...(token ? {Authorization: `Bearer ${token}`} : {}),
          },
          signal: abort.signal,
        })
        if (!response.ok || !response.body) {
          setStatus("lost")
          return
        }
        setStatus("waiting")

        const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
        let buffer = ""
        for (;;) {
          const {done, value} = await reader.read()
          if (done) break
          buffer += value
          // SSE frames are separated by a blank line; anything before the last
          // one is complete and anything after it is a partial frame that has
          // to survive until the next chunk arrives.
          const frames = buffer.split("\n\n")
          buffer = frames.pop() ?? ""
          for (const frame of frames) {
            const event = frame
              .split("\n")
              .find(line => line.startsWith("event:"))
              ?.slice(6)
              .trim()
            if (event === "paid") {
              setStatus("paid")
              abort.abort()
              return
            }
            // `closed` (the invoice can no longer be paid) and `timeout` (the
            // server's cap on stream life) are both the server saying it has
            // nothing more to report. Acting on them beats waiting for the body
            // to end, which never happens if an intermediary holds the socket.
            if (event === "closed" || event === "timeout") {
              setStatus("lost")
              abort.abort()
              return
            }
          }
        }
        // The server closed without saying "paid". Not an error the reader
        // can act on, but not a state to keep spinning in either.
        setStatus(current => (current === "paid" ? current : "lost"))
      } catch (error) {
        if ((error as Error).name !== "AbortError") setStatus("lost")
      }
    })()

    return () => abort.abort()
  }, [invoiceId, enabled])

  return status
}

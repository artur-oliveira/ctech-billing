"use client"

import {apiClient} from "@/lib/api/client"
import type {Checkout} from "@/lib/api/types"

/**
 * The payment link, screen X1. Two routes, no session.
 *
 * The signed token in the path is the whole of the authentication, so nothing
 * here sets an Authorization header — and the axios request interceptor only
 * adds one when a token happens to be in memory, which for a stranger opening
 * an e-mailed link it never is. A signed-in reader who opens somebody else's
 * link sends their token and it is ignored: the server addresses the invoice
 * from the link, never from the caller.
 */
export const checkoutKeys = {
  view: (token: string) => ["checkout", token] as const,
}

export async function getCheckout(token: string): Promise<Checkout> {
  const {data} = await apiClient.get<Checkout>(`/v1.0/checkout/${encodeURIComponent(token)}`)
  return data
}

export async function payCheckout(token: string): Promise<Checkout> {
  const {data} = await apiClient.post<Checkout>(
    `/v1.0/checkout/${encodeURIComponent(token)}/pay`
  )
  return data
}

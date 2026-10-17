"use client";

import {apiClient} from "@/lib/api/client";
import type {ConsoleScope} from "@/lib/console/merchant";

/**
 * Closing a merchant tenant (docs/specs/2026-10-17-merchant-tenant-deletion.md).
 * The console routes are the owner's, with the organization selected like every
 * billing call; the two link routes are public, the token being the credential.
 */
export type ClosureState =
  | "awaiting_confirmation" | "closing" | "blocked" | "ready_to_purge" | "purging" | "closed" | "cancelled" | "expired";

export interface Closure {
  request_id: string
  state: ClosureState
  confirm_by?: string
  grace_until?: string
  blockers?: string[]
}

export interface ClosureBlocker {
  code: string
  count: number
}

function scopeHeaders(s: ConsoleScope) {
  return {headers: {"X-Billing-Mode": s.mode, "X-Billing-Space": `org:${s.org}`}};
}

/** The phrase the owner types; the API checks it verbatim. */
export const CLOSURE_PHRASE = "ENCERRAR CONTA DE COBRANÇA";

export const closureKeys = {
  status: (s: ConsoleScope) => ["console", s.org, s.mode, "closure"] as const,
  blockers: (s: ConsoleScope) => ["console", s.org, s.mode, "closure", "blockers"] as const,
};

/** The open closure, or null when there is none (404). */
export async function getClosure(scope: ConsoleScope): Promise<Closure | null> {
  try {
    const {data} = await apiClient.get<Closure>("/v1.0/console/closure", scopeHeaders(scope));
    return data;
  } catch (err) {
    if ((err as {response?: {status?: number}})?.response?.status === 404) return null;
    throw err;
  }
}

export async function getClosureBlockers(scope: ConsoleScope): Promise<ClosureBlocker[]> {
  const {data} = await apiClient.get<{blockers: ClosureBlocker[]}>("/v1.0/console/closure/blockers", scopeHeaders(scope));
  return data.blockers;
}

export async function requestClosure(scope: ConsoleScope, phrase: string): Promise<Closure> {
  const {data} = await apiClient.post<Closure>("/v1.0/console/closure", {confirmation_phrase: phrase}, scopeHeaders(scope));
  return data;
}

export async function confirmClosure(requestID: string, token: string): Promise<Closure> {
  const {data} = await apiClient.post<Closure>("/v1.0/closure/confirm", {request_id: requestID, token});
  return data;
}

export async function cancelClosure(requestID: string, token: string): Promise<Closure> {
  const {data} = await apiClient.post<Closure>("/v1.0/closure/cancel", {request_id: requestID, token});
  return data;
}

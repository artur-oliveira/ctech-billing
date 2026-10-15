"use client";

import {useSyncExternalStore} from "react";

import {getAccessToken, subscribeAccessToken} from "@/lib/api/client";
import {USE_MOCK} from "@/lib/mockConfig";

/**
 * The `sub` of an access token, read without verifying it. It keys things that
 * belong to a person (the realtime cursor, a channel); it authorizes nothing,
 * the API verifies the token on every call and on the socket's first frame.
 */
export function subjectOf(token: string | null): string | null {
  if (!token) return null;
  try {
    const payload = token.split(".")[1]?.replace(/-/g, "+").replace(/_/g, "/");
    if (!payload) return null;
    const claims = JSON.parse(atob(payload)) as {sub?: unknown};
    return typeof claims.sub === "string" && claims.sub !== "" ? claims.sub : null;
  } catch {
    return null;
  }
}

const subscribe = (onChange: () => void) => subscribeAccessToken(onChange);
const snapshot = () => (USE_MOCK ? "mock" : subjectOf(getAccessToken()));

/** Who is signed in, or null before sign-in and after sign-out (re-read on every render). */
export function useSubject(): string | null {
  return useSyncExternalStore(subscribe, snapshot, () => null);
}

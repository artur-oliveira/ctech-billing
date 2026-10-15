import {API_BASE_URL, getAccessToken} from "@/lib/api/client";
import {spaceHeader} from "@/lib/console/space";

import type {Surface} from "./catalogue";
import type {ChangeView} from "./codec";
import type {RealtimeCore} from "./core";

export interface PollResult {
  changes: ChangeView[]
  head: string
  more: boolean
  /** Set instead of changes when the cursor cannot be replayed from. */
  gap?: string
}

/** The cursor route refused the credential: the token is dead, not the network. */
export class PollUnauthorized extends Error {}

/** A request more than this many pages long is a runaway, not a catch-up. */
const MAX_PAGES = 20;

export interface PollOptions {
  fetchChanges: (cursor: string) => Promise<PollResult>
  core: RealtimeCore
  intervalMs: number
  visible: () => boolean
}

/**
 * Catches up from the cursor over plain HTTP while the socket cannot. It runs
 * once at once and then every interval, only while the tab is visible (a hidden
 * tab has nobody to show it to), pages while the server says there is more, and
 * hands every change to the core as a `change` frame, so the same de-duplication
 * and revision rules apply as on the socket. It returns the function that stops
 * it, which the owner calls the moment the socket is live again.
 */
export function startPolling(opts: PollOptions): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const pollOnce = async (): Promise<void> => {
    for (let page = 0; page < MAX_PAGES; page++) {
      let result: PollResult;
      try {
        result = await opts.fetchChanges(opts.core.cursor);
      } catch (error) {
        // A dead token is told to the core (it waits for a new one); anything
        // else is the network, and the screens keep refetching on focus.
        if (error instanceof PollUnauthorized) opts.core.onFrame({type: "bye", reason: "unauthorized", retryAfterMs: 0});
        return;
      }
      if (stopped) return;
      if (result.gap) {
        opts.core.onFrame({type: "gap", reason: result.gap});
        return;
      }
      for (const change of result.changes) opts.core.onFrame({type: "change", change});
      // With no cursor yet, this response is "now": adopt its head.
      if (opts.core.cursor === "") opts.core.onFrame({type: "tick", head: result.head});
      if (!result.more) return;
    }
  };

  const loop = async (): Promise<void> => {
    if (stopped) return;
    if (opts.visible()) await pollOnce();
    if (stopped) return;
    timer = setTimeout(() => void loop(), opts.intervalMs);
  };

  void loop();
  return () => {
    stopped = true;
    if (timer) clearTimeout(timer);
  };
}

interface WireChange {
  id: string
  type: string
  entity: string
  entity_id: string
  status?: string
  revision: number
  occurred_at_ms: number
}

/**
 * The poll request for a surface: the same credential the socket's first frame
 * carries, in an Authorization header (never the URL), and the same selectors.
 * Plain fetch, not the axios client: its 401 handling navigates to the login page,
 * and here a 401 only means "wait for a new token".
 */
export function changesFetcher(surface: Surface): (cursor: string) => Promise<PollResult> {
  return async (cursor) => {
    const headers: Record<string, string> = {};
    switch (surface.kind) {
      case "checkout":
        headers.Authorization = `PayLink ${surface.token}`;
        break;
      case "console":
        headers.Authorization = `Bearer ${getAccessToken() ?? ""}`;
        headers["X-Billing-Space"] = `org:${surface.scope.org}`;
        headers["X-Billing-Mode"] = surface.scope.mode;
        break;
      case "finance":
        headers.Authorization = `Bearer ${getAccessToken() ?? ""}`;
        headers["X-Billing-Space"] = spaceHeader(surface.space);
        headers["X-Billing-Mode"] = surface.mode;
        break;
      default:
        headers.Authorization = `Bearer ${getAccessToken() ?? ""}`;
    }
    const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
    const res = await fetch(`${API_BASE_URL}/v1.0/realtime/changes${query}`, {headers, cache: "no-store"});
    if (res.status === 401 || res.status === 403) throw new PollUnauthorized();
    if (!res.ok) throw new Error(`changes: ${res.status}`);
    const body = (await res.json()) as {changes?: WireChange[]; head?: string; more?: boolean; gap?: string};
    return {
      changes: (body.changes ?? []).map((c) => ({
        id: c.id, type: c.type, entity: c.entity, entityId: c.entity_id, status: c.status ?? "",
        revision: c.revision, occurredAtMs: c.occurred_at_ms,
      })),
      head: body.head ?? "",
      more: body.more === true,
      gap: body.gap || undefined,
    };
  };
}

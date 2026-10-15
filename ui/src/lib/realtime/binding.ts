import {hashKey, type QueryClient, type QueryKey} from "@tanstack/react-query";

import {effectsOf, rootsOf, type Surface} from "./catalogue";
import type {ChangeView} from "./codec";

/** The most often one key is invalidated: a burst (a bulk classification) is one refetch per window. */
export const COALESCE_MS = 250;

interface Window {
  dirty: boolean
}

const windows = new WeakMap<QueryClient, Map<string, Window>>();

/**
 * Leading and trailing: the first change of a key invalidates at once, the rest of
 * the window collapse into one more when it closes, so a burst of 100 changes in
 * 50 ms costs two refetches of a key, not a hundred, and a lone change loses no time.
 */
function invalidateCoalesced(qc: QueryClient, queryKey: QueryKey): void {
  let open = windows.get(qc);
  if (!open) windows.set(qc, (open = new Map()));
  const hash = hashKey(queryKey);
  const current = open.get(hash);
  if (current) {
    current.dirty = true;
    return;
  }
  const entry: Window = {dirty: false};
  open.set(hash, entry);
  void qc.invalidateQueries({queryKey});
  const close = () => {
    if (entry.dirty) {
      entry.dirty = false;
      void qc.invalidateQueries({queryKey});
      setTimeout(close, COALESCE_MS);
    } else {
      open.delete(hash);
    }
  };
  setTimeout(close, COALESCE_MS);
}

/** Applies a change to the cache: invalidates what the catalogue says it touches. */
export function applyChange(qc: QueryClient, surface: Surface, c: ChangeView): void {
  for (const queryKey of effectsOf(surface, c).invalidate) invalidateCoalesced(qc, queryKey);
}

/** After a Gap: every query the surface holds is stale, and nothing else is touched. */
export function applyGap(qc: QueryClient, surface: Surface): void {
  for (const queryKey of rootsOf(surface)) {
    void qc.invalidateQueries({queryKey});
  }
}

import {hashKey, type QueryClient, type QueryKey} from "@tanstack/react-query";

import {effectsOf, rootsOf, type Surface} from "./catalogue";
import type {ChangeView} from "./codec";

/** The most often one key is invalidated: a burst (a bulk classification) is one refetch per window. */
export const COALESCE_MS = 250;

interface Window {
  dirty: boolean
}

const windows = new WeakMap<QueryClient, Map<string, Window>>();

interface Hold {
  count: number
  deferred: Map<string, QueryKey>
}

const holds = new WeakMap<QueryClient, Hold>();

/** A hold never outlives this, so a caller that forgets to release cannot mute the screens for good. */
export const HOLD_MAX_MS = 15_000;

/**
 * Defers what the server pushes while one intent is still half done: a bill
 * created already paid is announced (bill.updated) before its settle lands, and
 * the refetch that announcement causes paints the bill open for a moment. Held
 * changes are invalidated once, on release (idempotent; also released after
 * HOLD_MAX_MS). Writes' own invalidation (useFinanceMutation) is not affected.
 */
export function holdInvalidations(qc: QueryClient): () => void {
  let hold = holds.get(qc);
  if (!hold) holds.set(qc, (hold = {count: 0, deferred: new Map()}));
  const h = hold;
  h.count += 1;
  let released = false;
  const release = () => {
    if (released) return;
    released = true;
    clearTimeout(timer);
    h.count -= 1;
    if (h.count > 0) return;
    const keys = [...h.deferred.values()];
    h.deferred.clear();
    for (const queryKey of keys) invalidateCoalesced(qc, queryKey);
  };
  const timer = setTimeout(release, HOLD_MAX_MS);
  return release;
}

/**
 * Leading and trailing: the first change of a key invalidates at once, the rest of
 * the window collapse into one more when it closes, so a burst of 100 changes in
 * 50 ms costs two refetches of a key, not a hundred, and a lone change loses no time.
 */
function invalidateCoalesced(qc: QueryClient, queryKey: QueryKey): void {
  const hold = holds.get(qc);
  if (hold && hold.count > 0) {
    hold.deferred.set(hashKey(queryKey), queryKey);
    return;
  }
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

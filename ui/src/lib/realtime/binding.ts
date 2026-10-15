import type {QueryClient} from "@tanstack/react-query";

import {effectsOf, rootsOf, type Surface} from "./catalogue";
import type {ChangeView} from "./codec";

/** Applies a change to the cache: invalidates what the catalogue says it touches. */
export function applyChange(qc: QueryClient, surface: Surface, c: ChangeView): void {
  for (const queryKey of effectsOf(surface, c).invalidate) {
    void qc.invalidateQueries({queryKey});
  }
}

/** After a Gap: every query the surface holds is stale, and nothing else is touched. */
export function applyGap(qc: QueryClient, surface: Surface): void {
  for (const queryKey of rootsOf(surface)) {
    void qc.invalidateQueries({queryKey});
  }
}

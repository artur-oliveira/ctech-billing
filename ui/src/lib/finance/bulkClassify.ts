import {createIntent, type Intent} from "@/lib/api/idempotency";

/** What a bulk run sends for every line: the same "classificar como" and value. */
export type BulkBody = {category_id: string} | {transfer_account_id: string}

export interface BulkProgress {
  total: number
  done: number
  failed: number
}

export interface BulkResult {
  done: string[]
  failed: {id: string; error: unknown}[]
  /** True when the run was stopped before every line was started. */
  cancelled: boolean
}

export interface BulkOptions {
  /** Sends one classification: (line id, body, Idempotency-Key). */
  send: (id: string, body: BulkBody, key: string) => Promise<unknown>
  /** Lines in flight at once: the classifications are written one by one by the API. */
  concurrency?: number
  signal?: AbortSignal
  onProgress?: (p: BulkProgress) => void
  /**
   * One intent per line, kept by the caller across runs: a line that failed and
   * is tried again sends the same Idempotency-Key (a timeout followed by a
   * retry is one intent, never two classifications).
   */
  intents?: Map<string, Intent>
}

export const BULK_CONCURRENCY = 3;

/**
 * Classifies many lines the same way, client-side and under control: a few at a
 * time, each reported as it ends, one failure never stopping the rest, and a
 * stop that lets the lines already started finish instead of abandoning them
 * half-way.
 */
export async function runBulk(ids: readonly string[], body: BulkBody, opts: BulkOptions): Promise<BulkResult> {
  const { send, signal, onProgress } = opts;
  const intents = opts.intents ?? new Map<string, Intent>();
  const concurrency = Math.max(1, opts.concurrency ?? BULK_CONCURRENCY);
  const result: BulkResult = {done: [], failed: [], cancelled: false};
  let next = 0;

  const report = () => onProgress?.({total: ids.length, done: result.done.length, failed: result.failed.length});
  report();

  async function worker() {
    for (;;) {
      if (signal?.aborted) {
        result.cancelled = next < ids.length;
        return;
      }
      const i = next++;
      if (i >= ids.length) return;
      const id = ids[i];
      let intent = intents.get(id);
      if (!intent) intents.set(id, (intent = createIntent()));
      try {
        await send(id, body, intent.current());
        intent.reset();
        intents.delete(id);
        result.done.push(id);
      } catch (error) {
        result.failed.push({id, error});
      }
      report();
    }
  }

  await Promise.all(Array.from({length: Math.min(concurrency, ids.length)}, worker));
  if (signal?.aborted && result.done.length + result.failed.length < ids.length) result.cancelled = true;
  return result;
}

/** Whether a selection is all entradas, all saídas, or both (which only a transfer can take). */
export function directionOf(selection: readonly {direction: "credit" | "debit"}[]): "credit" | "debit" | "mixed" {
  if (selection.length === 0) return "mixed";
  const first = selection[0].direction;
  return selection.every(l => l.direction === first) ? first : "mixed";
}

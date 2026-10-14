import {describe, expect, it, vi} from "vitest";

import {directionOf, runBulk, type BulkProgress} from "./bulkClassify";

const lines = (n: number) => Array.from({length: n}, (_, i) => `tx${i + 1}`);
const tick = () => new Promise(r => setTimeout(r, 0));

describe("runBulk", () => {
  it("classifies every line with the same body and reports progress", async () => {
    const send = vi.fn().mockResolvedValue(undefined);
    const progress: BulkProgress[] = [];
    const res = await runBulk(lines(5), {category_id: "cat-1"}, {send, concurrency: 2, onProgress: p => progress.push(p)});
    expect(send).toHaveBeenCalledTimes(5);
    for (const call of send.mock.calls) expect(call[1]).toEqual({category_id: "cat-1"});
    expect(res.done).toHaveLength(5);
    expect(res.failed).toEqual([]);
    expect(progress.at(-1)).toMatchObject({total: 5, done: 5, failed: 0});
  });

  it("never has more than the concurrency in flight", async () => {
    let inFlight = 0;
    let peak = 0;
    const send = vi.fn(async () => {
      inFlight++;
      peak = Math.max(peak, inFlight);
      await tick();
      inFlight--;
    });
    await runBulk(lines(10), {category_id: "c"}, {send, concurrency: 3});
    expect(peak).toBe(3);
  });

  it("keeps going when one line fails, and says which and why", async () => {
    const send = vi.fn(async (id: string) => {
      if (id === "tx2") throw new Error("a conta foi arquivada");
    });
    const res = await runBulk(lines(4), {category_id: "c"}, {send, concurrency: 1});
    expect(res.done).toEqual(["tx1", "tx3", "tx4"]);
    expect(res.failed).toEqual([{id: "tx2", error: expect.objectContaining({message: "a conta foi arquivada"})}]);
  });

  it("stops starting new lines when cancelled, and lets the ones in flight finish", async () => {
    const ctl = new AbortController();
    const send = vi.fn(async (id: string) => {
      await tick();
      if (id === "tx2") ctl.abort();
    });
    const res = await runBulk(lines(10), {category_id: "c"}, {send, concurrency: 1, signal: ctl.signal});
    expect(res.cancelled).toBe(true);
    expect(res.done).toEqual(["tx1", "tx2"]);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("sends the same Idempotency-Key for a line on a retry, and a new one for the next intent", async () => {
    const keys = new Map<string, string[]>();
    let fail = true;
    const send = vi.fn(async (id: string, _body: unknown, key: string) => {
      keys.set(id, [...(keys.get(id) ?? []), key]);
      if (id === "tx1" && fail) throw new Error("timeout");
    });
    const intents = new Map();
    const first = await runBulk(["tx1", "tx2"], {category_id: "c"}, {send, concurrency: 1, intents});
    expect(first.failed.map(f => f.id)).toEqual(["tx1"]);
    fail = false;
    await runBulk(first.failed.map(f => f.id), {category_id: "c"}, {send, concurrency: 1, intents});
    const tx1 = keys.get("tx1") ?? [];
    expect(tx1).toHaveLength(2);
    expect(tx1[0]).toBe(tx1[1]);
    expect(keys.get("tx2")).toHaveLength(1);
  });
});

describe("directionOf", () => {
  it("names a selection as entradas, saídas or both", () => {
    expect(directionOf([{direction: "credit"}, {direction: "credit"}])).toBe("credit");
    expect(directionOf([{direction: "debit"}])).toBe("debit");
    expect(directionOf([{direction: "credit"}, {direction: "debit"}])).toBe("mixed");
    expect(directionOf([])).toBe("mixed");
  });
});

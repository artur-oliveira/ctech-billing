import {describe, expect, it} from "vitest";

import {decode, type Inbound, MAX_FRAME_BYTES} from "./codec";

// A small seeded generator (mulberry32), so a failure reproduces.
function rng(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const KNOWN: Inbound["type"][] = ["ready", "change", "gap", "pong", "tick", "bye", "hint", "unknown"];

describe("decode on random bytes", () => {
  it("returns a known frame or throws an Error, never anything else", () => {
    const next = rng(20261015);
    for (let i = 0; i < 10_000; i++) {
      const bytes = new Uint8Array(Math.floor(next() * 200));
      for (let j = 0; j < bytes.length; j++) bytes[j] = Math.floor(next() * 256);
      try {
        const frame = decode(bytes.buffer as ArrayBuffer);
        expect(KNOWN).toContain(frame.type);
      } catch (error) {
        expect(error).toBeInstanceOf(Error);
      }
    }
  });

  it("refuses a frame over the cap before parsing it", () => {
    expect(() => decode(new ArrayBuffer(MAX_FRAME_BYTES + 1))).toThrow(RangeError);
  });

  it("treats an empty frame as unknown, not an error", () => {
    expect(decode(new ArrayBuffer(0)).type).toBe("unknown");
  });
});

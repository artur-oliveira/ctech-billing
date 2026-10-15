import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

import {encode} from "@/lib/realtime/codec";
import {ServerFrame} from "@/lib/realtime/wire/realtime";

import {installRealtimeMock} from "./realtimeMock";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("realtime mock", () => {
  it("answers Ready to the Auth and then scripts payments", async () => {
    installRealtimeMock();
    const socket = new window.WebSocket("wss://mock/v1.0/ws");
    socket.binaryType = "arraybuffer";
    const kinds: string[] = [];
    socket.onmessage = (e) => void kinds.push(ServerFrame.decode(new Uint8Array(e.data as ArrayBuffer)).body?.$case ?? "");
    await vi.advanceTimersByTimeAsync(60);
    socket.send(encode({type: "auth", token: "t"}) as Uint8Array<ArrayBuffer>);
    await vi.advanceTimersByTimeAsync(8_100);
    expect(kinds).toEqual(["ready", "change"]);
    socket.close();
  });
});

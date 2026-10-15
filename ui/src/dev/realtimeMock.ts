// Mock mode has no API, so no socket: this stands in for the browser's
// WebSocket and plays the server. It accepts the Auth, answers Ready and then
// scripts a payment every few seconds on the mock invoices, so the screens'
// live behaviour (rows flipping in place, the "Ao vivo" indicator) can be seen
// and designed without a backend. Installed once, by MockControls.

import {ClientFrame, ServerFrame} from "@/lib/realtime/wire/realtime";

const PAYMENT_EVERY_MS = 8_000;

type Body = NonNullable<ServerFrame["body"]>;

class MockRealtimeSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockRealtimeSocket.CONNECTING;
  binaryType: BinaryType = "blob";
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;

  private timer: ReturnType<typeof setInterval> | undefined;
  private sequence = 0;

  constructor(readonly url: string) {
    setTimeout(() => {
      if (this.readyState !== MockRealtimeSocket.CONNECTING) return;
      this.readyState = MockRealtimeSocket.OPEN;
      this.onopen?.({} as Event);
    }, 50);
  }

  send(data: ArrayBuffer | ArrayBufferView): void {
    const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    const body = ClientFrame.decode(bytes).body;
    if (body?.$case === "auth") this.start();
    if (body?.$case === "ping") this.push({$case: "pong", value: {}} as Body);
  }

  close(code = 1000): void {
    if (this.readyState === MockRealtimeSocket.CLOSED) return;
    clearInterval(this.timer);
    this.readyState = MockRealtimeSocket.CLOSED;
    this.onclose?.({code} as CloseEvent);
  }

  private start(): void {
    this.push({$case: "ready", value: {protocol_version: 1, server_time_ms: Date.now(), scope_kinds: ["tenant"]}});
    this.timer = setInterval(() => this.pay(), PAYMENT_EVERY_MS);
  }

  /** The next open mock invoice is paid; the screens refetch and the mock API answers as it always does. */
  private pay(): void {
    this.sequence += 1;
    const id = `mock-${String(Date.now()).padStart(14, "0")}-${this.sequence}`;
    this.push({
      $case: "change",
      value: {id, type: "invoice.paid", entity: "invoice", entity_id: `in_mock_${this.sequence}`, status: "PAID", revision: 1, occurred_at_ms: Date.now()},
    });
  }

  private push(body: Body): void {
    const bytes = ServerFrame.encode({body}).finish();
    const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
    this.onmessage?.({data: buffer} as MessageEvent);
  }
}

let installed = false;

/** Replaces the global WebSocket with the scripted server. Safe to call twice. */
export function installRealtimeMock(): void {
  if (installed || typeof window === "undefined") return;
  installed = true;
  window.WebSocket = MockRealtimeSocket as unknown as typeof WebSocket;
}

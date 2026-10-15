// Test support: a scripted WebSocket replacement. Install it with
// vi.stubGlobal("WebSocket", FakeWebSocket) and drive the "server" side by hand.

export class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  /** Every socket the code under test has created, oldest first. */
  static instances: FakeWebSocket[] = [];

  static reset(): void {
    FakeWebSocket.instances = [];
  }

  static get last(): FakeWebSocket {
    const s = FakeWebSocket.instances.at(-1);
    if (!s) throw new Error("no socket was created");
    return s;
  }

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  binaryType: BinaryType = "blob";
  /** What the client sent, as raw bytes. */
  sent: Uint8Array[] = [];
  closeCode: number | null = null;

  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  send(data: ArrayBuffer | ArrayBufferView | string): void {
    if (this.readyState !== FakeWebSocket.OPEN) throw new Error("send on a socket that is not open");
    if (typeof data === "string") throw new Error("the realtime protocol is binary");
    const view = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    this.sent.push(new Uint8Array(view));
  }

  close(code = 1000): void {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.closeCode = code;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({code} as CloseEvent);
  }

  // ── the server's side ──────────────────────────────────────────────────────

  /** The handshake completed. */
  open(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.({} as Event);
  }

  /** The server sends a frame (protobuf bytes). */
  receive(bytes: Uint8Array): void {
    const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
    this.onmessage?.({data: buffer} as MessageEvent);
  }

  /** The connection drops without the client asking. */
  drop(code = 1006): void {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({code} as CloseEvent);
  }
}

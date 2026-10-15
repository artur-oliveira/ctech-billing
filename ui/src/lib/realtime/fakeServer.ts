// Test support: a scripted realtime server speaking real protobuf over a
// FakeWebSocket. It can drop the connection, say Bye, send frames duplicated or
// out of order, send a Gap, run ahead with a Tick, and hold Ready back.

import type {FakeWebSocket} from "./fakeSocket";
import {ClientFrame, ServerFrame} from "./wire/realtime";

type Body = NonNullable<ServerFrame["body"]>;

const frame = (body: Body) => ServerFrame.encode({body}).finish();

export class FakeServer {
  constructor(readonly socket: FakeWebSocket) {}

  private push(body: Body): void {
    this.socket.receive(frame(body));
  }

  /** The handshake completes (the client then sends its Auth). */
  open(): this {
    this.socket.open();
    return this;
  }

  ready(protocolVersion = 1): this {
    this.push({$case: "ready", value: {protocol_version: protocolVersion, server_time_ms: Date.now(), scope_kinds: ["tenant"]}});
    return this;
  }

  change(id: string, entityId = "in_1", revision = 1, type = "invoice.paid", entity = "invoice"): this {
    this.push({$case: "change", value: {id, type, entity, entity_id: entityId, status: "PAID", revision, occurred_at_ms: 1}});
    return this;
  }

  gap(reason: string): this {
    this.push({$case: "gap", value: {reason}});
    return this;
  }

  tick(head: string): this {
    this.push({$case: "tick", value: {head}});
    return this;
  }

  hint(pollIntervalMs: number): this {
    this.push({$case: "hint", value: {poll_interval_ms: pollIntervalMs}});
    return this;
  }

  bye(reason: string, retryAfterMs = 0): this {
    this.push({$case: "bye", value: {reason, retry_after_ms: retryAfterMs}});
    return this;
  }

  /** Closes the connection from the server's side, as it does after a Bye. */
  close(code = 1001): this {
    this.socket.drop(code);
    return this;
  }

  /** What the client has sent, decoded. */
  get received(): NonNullable<ClientFrame["body"]>[] {
    return this.socket.sent.flatMap((bytes) => {
      const body = ClientFrame.decode(bytes).body;
      return body ? [body] : [];
    });
  }

  /** The Auth the client opened with, if it has sent one. */
  get auth() {
    const first = this.received.find((b) => b.$case === "auth");
    return first?.$case === "auth" ? first.value : undefined;
  }
}

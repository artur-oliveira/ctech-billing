import {ClientFrame, ServerFrame} from "./wire/realtime";

/** The protocol version this client speaks (docs/realtime/protocol.md). */
export const CLIENT_PROTOCOL_VERSION = 1;

/** The server's own cap on a frame: a bigger one is not decoded. */
export const MAX_FRAME_BYTES = 32 * 1024;

export interface ChangeView {
  id: string
  type: string
  entity: string
  entityId: string
  status: string
  revision: number
  occurredAtMs: number
}

export type Inbound =
  | {type: "ready"; protocolVersion: number; serverTimeMs: number; scopeKinds: string[]}
  | {type: "change"; change: ChangeView}
  | {type: "gap"; reason: string}
  | {type: "pong"} // the shape @aoctech/ws-client's heartbeat looks for
  | {type: "tick"; head: string}
  | {type: "bye"; reason: string; retryAfterMs: number}
  | {type: "hint"; pollIntervalMs: number}
  /** A frame from a newer server that this client does not know: ignored, never an error. */
  | {type: "unknown"}

export type Outbound =
  | {type: "auth"; token?: string; payLink?: string; space?: string; mode?: "live" | "test"; resumeCursor?: string}
  | {type: "reauth"; token: string}
  | {type: "ping"} // what the shared hook sends every 20 s

/** Plain frame to protobuf bytes. */
export function encode(o: Outbound): Uint8Array {
  let frame: ClientFrame;
  switch (o.type) {
    case "auth":
      frame = {
        body: {
          $case: "auth",
          value: {
            token: o.token ?? "",
            pay_link: o.payLink ?? "",
            space: o.space ?? "",
            resume_cursor: o.resumeCursor ?? "",
            client_protocol_version: CLIENT_PROTOCOL_VERSION,
            mode: o.mode ?? "",
          },
        },
      };
      break;
    case "reauth":
      frame = {body: {$case: "reauth", value: {token: o.token}}};
      break;
    case "ping":
      frame = {body: {$case: "ping", value: {client_time_ms: Date.now()}}};
      break;
  }
  return ClientFrame.encode(frame).finish();
}

/**
 * Protobuf bytes to a plain frame. Throws on a malformed or oversize frame (the
 * shared hook ignores a frame that throws); returns `{type: "unknown"}` for a
 * well-formed frame whose body this client does not know.
 */
export function decode(data: ArrayBuffer): Inbound {
  if (data.byteLength > MAX_FRAME_BYTES) {
    throw new RangeError(`realtime frame of ${data.byteLength} bytes is over the ${MAX_FRAME_BYTES} byte cap`);
  }
  const body = ServerFrame.decode(new Uint8Array(data)).body;
  switch (body?.$case) {
    case "ready":
      return {
        type: "ready",
        protocolVersion: body.value.protocol_version,
        serverTimeMs: body.value.server_time_ms,
        scopeKinds: body.value.scope_kinds,
      };
    case "change":
      return {
        type: "change",
        change: {
          id: body.value.id,
          type: body.value.type,
          entity: body.value.entity,
          entityId: body.value.entity_id,
          status: body.value.status,
          revision: body.value.revision,
          occurredAtMs: body.value.occurred_at_ms,
        },
      };
    case "gap":
      return {type: "gap", reason: body.value.reason};
    case "pong":
      return {type: "pong"};
    case "tick":
      return {type: "tick", head: body.value.head};
    case "bye":
      return {type: "bye", reason: body.value.reason, retryAfterMs: body.value.retry_after_ms};
    case "hint":
      return {type: "hint", pollIntervalMs: body.value.poll_interval_ms};
    default:
      return {type: "unknown"};
  }
}

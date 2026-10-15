import {readFileSync} from "node:fs";
import {join} from "node:path";

import {describe, expect, it} from "vitest";

import {CLIENT_PROTOCOL_VERSION, decode, encode, MAX_FRAME_BYTES} from "./codec";
import {ClientFrame} from "./wire/realtime";

const DIR = join(__dirname, "../../../../proto/testdata");
const fixture = (name: string) => new Uint8Array(readFileSync(join(DIR, `${name}.bin`)));
const buffer = (bytes: Uint8Array) => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;

describe("decode, against the contract's reference bytes", () => {
  it("reads every server frame the way its name says", () => {
    expect(decode(buffer(fixture("ready")))).toEqual({
      type: "ready", protocolVersion: 1, serverTimeMs: 1_760_000_000_000, scopeKinds: ["tenant", "invoice"],
    });
    expect(decode(buffer(fixture("change_invoice_paid")))).toEqual({
      type: "change",
      change: {
        id: "01JABCDEFGHJKMNPQRSTVWXYZ0", type: "invoice.paid", entity: "invoice",
        entityId: "in_01JABCDEFGHJKMNPQRSTVWXYZ1", status: "PAID", revision: 3, occurredAtMs: 1_760_000_000_000,
      },
    });
    expect(decode(buffer(fixture("change_minimal")))).toMatchObject({
      type: "change", change: {id: "01JABCDEFGHJKMNPQRSTVWXYZ0", type: "customer.updated", entityId: "", revision: 0},
    });
    expect(decode(buffer(fixture("gap_cursor_too_old")))).toEqual({type: "gap", reason: "cursor_too_old"});
    expect(decode(buffer(fixture("pong")))).toEqual({type: "pong"});
    expect(decode(buffer(fixture("tick")))).toEqual({type: "tick", head: "01JABCDEFGHJKMNPQRSTVWXYZ0"});
    expect(decode(buffer(fixture("bye_draining")))).toEqual({type: "bye", reason: "draining", retryAfterMs: 2000});
    expect(decode(buffer(fixture("hint")))).toEqual({type: "hint", pollIntervalMs: 15000});
  });

  it("decodes a change that carries a field from a newer server", () => {
    expect(decode(buffer(fixture("change_with_unknown_field")))).toMatchObject({
      type: "change", change: {type: "invoice.paid", status: "PAID", revision: 4},
    });
  });

  it("returns a no-op sentinel, not an error, for a frame with no body this client knows", () => {
    // A ServerFrame whose only content is an unknown field 99.
    expect(decode(buffer(new Uint8Array([0xda, 0x06, 0x01, 0x78])))).toEqual({type: "unknown"});
    expect(decode(buffer(new Uint8Array([])))).toEqual({type: "unknown"});
  });

  it("refuses a frame over the server's own cap before decoding it", () => {
    expect(() => decode(new ArrayBuffer(MAX_FRAME_BYTES + 1024))).toThrow(/cap/);
    expect(() => decode(new ArrayBuffer(MAX_FRAME_BYTES))).not.toThrow(/cap/);
  });

  it("throws on a malformed frame", () => {
    expect(() => decode(buffer(new Uint8Array([0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f])))).toThrow();
  });
});

describe("encode", () => {
  it("writes an Auth frame byte for byte as the contract's reference", () => {
    const bytes = encode({
      type: "auth", token: "eyJhbGciOiJSUzI1NiJ9.e30.sig", space: "org:01a04ed6-1af9-745e-bcf7-d3b66fe52321",
      resumeCursor: "01JABCDEFGHJKMNPQRSTVWXYZ0", mode: "live",
    });
    expect(Buffer.from(bytes).equals(Buffer.from(fixture("auth_token")))).toBe(true);
    expect(ClientFrame.decode(bytes).body).toMatchObject({$case: "auth", value: {client_protocol_version: CLIENT_PROTOCOL_VERSION}});
  });

  it("writes a pay-link Auth with no token", () => {
    const bytes = encode({type: "auth", payLink: "ctech.2.b64payload.b64mac"});
    expect(Buffer.from(bytes).equals(Buffer.from(fixture("auth_pay_link")))).toBe(true);
  });

  it("writes Reauth and Ping", () => {
    expect(Buffer.from(encode({type: "reauth", token: "eyJhbGciOiJSUzI1NiJ9.e30.sig2"})).equals(Buffer.from(fixture("reauth")))).toBe(true);
    const ping = ClientFrame.decode(encode({type: "ping"})).body;
    expect(ping?.$case).toBe("ping");
    if (ping?.$case === "ping") expect(ping.value.client_time_ms).toBeGreaterThan(1_700_000_000_000);
  });
});

import {readdirSync, readFileSync} from "node:fs";
import {join} from "node:path";

import {describe, expect, it} from "vitest";

import {ClientFrame, ServerFrame} from "./realtime";

const DIR = join(__dirname, "../../../../../proto/testdata");
const FIXTURE_COUNT = 13;
const UNKNOWN_FIELD_FIXTURE = "change_with_unknown_field.bin";

const files = readdirSync(DIR).filter((f) => f.endsWith(".bin"));
const isClientFrame = (name: string) => name.startsWith("auth") || name.startsWith("reauth") || name === "ping.bin";

describe("the contract's reference bytes", () => {
  it("is the whole fixture set", () => {
    expect(files).toHaveLength(FIXTURE_COUNT);
  });

  it.each(files.filter((f) => f !== UNKNOWN_FIELD_FIXTURE))("%s decodes and re-encodes to the same bytes", (name) => {
    const raw = readFileSync(join(DIR, name));
    const codec = isClientFrame(name) ? ClientFrame : ServerFrame;
    const again = Buffer.from(codec.encode(codec.decode(raw) as never).finish());
    expect(again.equals(raw)).toBe(true);
  });

  it("skips an unknown field instead of rejecting the frame", () => {
    const frame = ServerFrame.decode(readFileSync(join(DIR, UNKNOWN_FIELD_FIXTURE)));
    expect(frame.body?.$case).toBe("change");
    if (frame.body?.$case === "change") {
      expect(frame.body.value.id).not.toBe("");
      expect(frame.body.value.type).toBe("invoice.paid");
    }
  });

  it("reads a change as the server wrote it", () => {
    const frame = ServerFrame.decode(readFileSync(join(DIR, "change_invoice_paid.bin")));
    expect(frame.body).toMatchObject({
      $case: "change",
      value: {type: "invoice.paid", entity: "invoice", status: "PAID", revision: 3},
    });
  });
});

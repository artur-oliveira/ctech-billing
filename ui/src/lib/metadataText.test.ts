import {describe, expect, it} from "vitest";

import {parseMetadataText} from "./metadataText";

describe("parseMetadataText", () => {
  it("reads key=value lines, skipping blanks and keeping '=' inside a value", () => {
    expect(parseMetadataText("plan=pro\n\n quota_nfe = 1200 \nnote=a=b")).toEqual({
      ok: true,
      value: {plan: "pro", quota_nfe: "1200", note: "a=b"},
    });
  });

  it("is empty for empty text", () => {
    expect(parseMetadataText("  \n")).toEqual({ok: true, value: {}});
  });

  it("names the line of a bad one", () => {
    expect(parseMetadataText("plan=pro\nsemigual")).toEqual({ok: false, line: 2, reason: "format"});
    expect(parseMetadataText("=x")).toEqual({ok: false, line: 1, reason: "format"});
    expect(parseMetadataText("a=1\na=2")).toEqual({ok: false, line: 2, reason: "duplicate"});
    expect(parseMetadataText(`${"k".repeat(41)}=1`)).toEqual({ok: false, line: 1, reason: "key"});
    expect(parseMetadataText(`k=${"v".repeat(501)}`)).toEqual({ok: false, line: 1, reason: "value"});
  });
});

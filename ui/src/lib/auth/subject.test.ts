import {describe, expect, it} from "vitest";

import {subjectOf} from "./subject";

const jwt = (claims: object) => `h.${btoa(JSON.stringify(claims)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")}.s`;

describe("subjectOf", () => {
  it("reads sub", () => expect(subjectOf(jwt({sub: "u1"}))).toBe("u1"));
  it("is null for nothing, junk or a missing sub", () => {
    expect(subjectOf(null)).toBeNull();
    expect(subjectOf("junk")).toBeNull();
    expect(subjectOf("a.%%%.c")).toBeNull();
    expect(subjectOf(jwt({}))).toBeNull();
    expect(subjectOf(jwt({sub: 5}))).toBeNull();
  });
});

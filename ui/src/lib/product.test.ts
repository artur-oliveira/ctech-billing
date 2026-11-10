import {beforeEach, describe, expect, it, vi} from "vitest";

import {entryHref, rememberProduct} from "@/lib/product";

describe("entryHref", () => {
  beforeEach(() => window.localStorage.clear());

  it("is the portal when nothing is stored", () => {
    expect(entryHref()).toBe("/dashboard");
  });

  it("returns the remembered product's home", () => {
    rememberProduct("finance");
    expect(entryHref()).toBe("/finance");
    rememberProduct("console");
    expect(entryHref()).toBe("/console/overview");
  });

  it("ignores a value outside the whitelist", () => {
    for (const bad of ["admin", "__proto__", "toString", "/evil", ""]) {
      window.localStorage.setItem("ctech-billing-last-product", bad);
      expect(entryHref()).toBe("/dashboard");
    }
  });

  it("survives blocked storage", () => {
    const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => rememberProduct("finance")).not.toThrow();
    expect(entryHref()).toBe("/dashboard");
    get.mockRestore();
    set.mockRestore();
  });
});

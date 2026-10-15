import {describe, expect, it} from "vitest";

import {createCursorStore, type KeyValueStorage} from "./cursor";

function memoryStorage(): KeyValueStorage & {data: Map<string, string>} {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
}

const throwing: KeyValueStorage = {
  getItem() { throw new Error("private mode"); },
  setItem() { throw new Error("private mode"); },
  removeItem() { throw new Error("private mode"); },
};

describe("cursor store", () => {
  it("is empty on the first run and remembers what is set", () => {
    const store = createCursorStore("c:one", memoryStorage());
    expect(store.get()).toBe("");
    store.set("01A");
    expect(store.get()).toBe("01A");
  });

  it("survives a new store over the same storage (a reload)", () => {
    const storage = memoryStorage();
    createCursorStore("c:one", storage).set("01B");
    expect(createCursorStore("c:one", storage).get()).toBe("01B");
  });

  it("never reads another principal's cursor", () => {
    const storage = memoryStorage();
    createCursorStore("console:org-a:live", storage).set("01C");
    expect(createCursorStore("console:org-b:live", storage).get()).toBe("");
    expect(createCursorStore("console:org-a:test", storage).get()).toBe("");
  });

  it("clears", () => {
    const storage = memoryStorage();
    const store = createCursorStore("c:one", storage);
    store.set("01D");
    store.clear();
    expect(store.get()).toBe("");
    expect(createCursorStore("c:one", storage).get()).toBe("");
  });

  it("degrades to memory when storage throws, without throwing", () => {
    const store = createCursorStore("c:one", throwing);
    expect(() => store.set("01E")).not.toThrow();
    expect(store.get()).toBe("01E");
    expect(() => store.clear()).not.toThrow();
    expect(store.get()).toBe("");
  });

  it("keeps a cursor only if it is the newest: an older one never moves it back", () => {
    const store = createCursorStore("c:one", memoryStorage());
    store.set("01F");
    store.set("01A");
    expect(store.get()).toBe("01F");
  });
});

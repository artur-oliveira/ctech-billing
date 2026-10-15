/** The part of Web Storage the cursor needs; sessionStorage satisfies it. */
export interface KeyValueStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

export interface CursorStore {
  get(): string
  /** Moves the cursor forward; an older id never moves it back. */
  set(cursor: string): void
  clear(): void
}

const PREFIX = "billing:rt:cursor:";

function defaultStorage(): KeyValueStorage | null {
  try {
    return typeof sessionStorage === "undefined" ? null : sessionStorage;
  } catch {
    return null; // storage blocked: the cursor lives in memory
  }
}

/**
 * Where a connection remembers how far it has read. Stored per `key`, which
 * names the principal, the mode and the space, so a cursor is never read by
 * another one. Every storage access is guarded: in private mode or with storage
 * blocked the cursor degrades to memory, and a reload simply starts from now.
 * Cursors are ULIDs, so "newer" is a string comparison.
 */
export function createCursorStore(key: string, storage: KeyValueStorage | null = defaultStorage()): CursorStore {
  const storageKey = PREFIX + key;
  let memory = "";

  const read = (): string => {
    try {
      const stored = storage?.getItem(storageKey);
      if (typeof stored === "string" && stored > memory) memory = stored;
    } catch {
      // keep what memory has
    }
    return memory;
  };

  return {
    get: read,
    set(cursor) {
      if (cursor <= read()) return;
      memory = cursor;
      try {
        storage?.setItem(storageKey, cursor);
      } catch {
        // memory only
      }
    },
    clear() {
      memory = "";
      try {
        storage?.removeItem(storageKey);
      } catch {
        // memory only
      }
    },
  };
}

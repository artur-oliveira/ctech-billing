/**
 * An insertion-ordered set that forgets its oldest member past `capacity`, so
 * remembering what was applied never grows without bound.
 */
export class BoundedSet<T> {
  private readonly items = new Set<T>();

  constructor(private readonly capacity: number) {}

  has(item: T): boolean {
    return this.items.has(item);
  }

  /** Adds the item; false when it was already there. */
  add(item: T): boolean {
    if (this.items.has(item)) return false;
    this.items.add(item);
    if (this.items.size > this.capacity) {
      const oldest = this.items.values().next();
      if (!oldest.done) this.items.delete(oldest.value);
    }
    return true;
  }

  get size(): number {
    return this.items.size;
  }
}

/** The same bound for a map: setting a key moves it to the newest end. */
export class BoundedMap<K, V> {
  private readonly items = new Map<K, V>();

  constructor(private readonly capacity: number) {}

  get(key: K): V | undefined {
    return this.items.get(key);
  }

  set(key: K, value: V): void {
    this.items.delete(key);
    this.items.set(key, value);
    if (this.items.size > this.capacity) {
      const oldest = this.items.keys().next();
      if (!oldest.done) this.items.delete(oldest.value);
    }
  }
}

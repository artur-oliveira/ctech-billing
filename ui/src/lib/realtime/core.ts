import type {ChangeView, Inbound, Outbound} from "./codec";
import type {CursorStore} from "./cursor";
import {BoundedMap, BoundedSet} from "./seen";

/** What the core drives: the production one wraps @aoctech/ws-client's hook. */
export interface Transport {
  send(o: Outbound): boolean
  /** Opens a fresh connection now, with no backoff (it re-sends Auth with the cursor). */
  reconnect(): void
  close(): void
}

export type RealtimeStatus = "off" | "connecting" | "live" | "polling" | "waiting-token" | "revoked"

export interface CoreOptions {
  scopeKind: "console" | "portal" | "checkout" | "finance"
  cursor: CursorStore
  /** The binding: a change that has not been applied before. */
  onChange: (c: ChangeView) => void
  onGap: (reason: string) => void
  onStatus: (s: RealtimeStatus) => void
  /** True while the connection must not be reopened (a draining server, a dead token). */
  onHold: (held: boolean) => void
  /** Spreads reconnects after a deploy; 0 to 2000 ms. Injectable for tests. */
  jitter?: () => number
}

type AuthFields = {token?: string; payLink?: string; space?: string; mode?: "live" | "test"}

const SEEN_CAPACITY = 500;
const REVISION_CAPACITY = 2000;
const MAX_JITTER_MS = 2000;

/**
 * The React-free heart of the client. It turns frames into changes the screens
 * apply exactly once and in order, remembers how far it has read, and decides
 * what a Gap, a Tick or a Bye asks of the connection. It never touches a socket:
 * it talks to a Transport, and to its owner through callbacks.
 */
export class RealtimeCore {
  private transport: Transport | null = null;
  private readonly seen = new BoundedSet<string>(SEEN_CAPACITY);
  private readonly revisions = new BoundedMap<string, number>(REVISION_CAPACITY);
  private holdTimer: ReturnType<typeof setTimeout> | null = null;
  private held = false;
  private replayPending = false;
  private current: RealtimeStatus = "connecting";

  /** The version the server announced in Ready; 0 until it has. */
  protocolVersion = 0;
  /** How often to poll while status is "polling"; from the server's Hint. */
  pollIntervalMs = 0;

  constructor(private readonly opts: CoreOptions) {}

  get cursor(): string {
    return this.opts.cursor.get();
  }

  get status(): RealtimeStatus {
    return this.current;
  }

  attach(t: Transport): void {
    this.transport = t;
  }

  private setStatus(s: RealtimeStatus): void {
    if (s === this.current) return;
    this.current = s;
    this.opts.onStatus(s);
  }

  private hold(): void {
    if (!this.held) {
      this.held = true;
      this.opts.onHold(true);
    }
  }

  private release(): void {
    if (this.holdTimer) clearTimeout(this.holdTimer);
    this.holdTimer = null;
    if (this.held) {
      this.held = false;
      this.opts.onHold(false);
    }
  }

  /** The socket opened: introduce ourselves, resuming from the cursor if there is one. */
  onOpen(auth: AuthFields): void {
    this.replayPending = false;
    const cursor = this.cursor;
    this.transport?.send({type: "auth", ...auth, ...(cursor ? {resumeCursor: cursor} : {})});
  }

  /** The socket closed. The shared hook reconnects on its own backoff unless we hold it. */
  onClose(): void {
    if (this.current === "waiting-token" || this.current === "revoked" || this.held) return;
    this.setStatus("connecting");
  }

  onFrame(f: Inbound): void {
    switch (f.type) {
      case "ready":
        this.protocolVersion = f.protocolVersion;
        this.setStatus("live");
        return;
      case "change":
        this.applyChange(f.change);
        return;
      case "gap":
        this.onGap(f.reason);
        return;
      case "tick":
        this.onTick(f.head);
        return;
      case "hint":
        this.pollIntervalMs = f.pollIntervalMs;
        if (f.pollIntervalMs > 0) this.setStatus("polling");
        else if (this.current === "polling") this.setStatus("live");
        return;
      case "bye":
        this.onBye(f.reason, f.retryAfterMs);
        return;
      case "pong":
      case "unknown":
        return;
    }
  }

  private applyChange(c: ChangeView): void {
    // De-duplicated by id: a replay overlaps the cursor on purpose, and a reload
    // resumes from it. Seen is checked before the cursor, so an id older than the
    // cursor that was never applied (the overlap) still is, once.
    if (!this.seen.add(c.id)) return;
    this.opts.cursor.set(c.id); // an older id never moves the cursor back

    // Ordered by revision per entity. A change with no revision cannot be
    // ordered, so it is applied rather than compared.
    if (c.revision > 0) {
      const key = `${c.entity}#${c.entityId}`;
      const last = this.revisions.get(key);
      if (last !== undefined && c.revision < last) return;
      this.revisions.set(key, Math.max(last ?? 0, c.revision));
    }
    this.opts.onChange(c);
  }

  private onGap(reason: string): void {
    // After "slow_consumer" the server closes the socket and the reconnect replays
    // what was dropped from the cursor we still hold. After "cursor_too_old" or
    // "replay_limit" the cursor cannot be replayed from, and keeping it would make
    // every Tick ask again and be refused again: it is dropped (the screens reload
    // everything on a Gap) and the next Tick sets it.
    if (reason !== "slow_consumer") this.opts.cursor.clear();
    this.opts.onGap(reason);
  }

  private onTick(head: string): void {
    const cursor = this.cursor;
    if (head === "" || head <= cursor) return;
    if (cursor === "") {
      // Nothing to replay (a first run, or right after a Gap): this is "now".
      this.opts.cursor.set(head);
      return;
    }
    // The server knows something newer than we have read: ask for a replay, once.
    if (this.replayPending) return;
    this.replayPending = true;
    this.transport?.reconnect();
  }

  private onBye(reason: string, retryAfterMs: number): void {
    switch (reason) {
      case "draining": {
        // The server is going away. Wait its retry-after plus jitter so a deploy
        // does not send every client back at the same instant.
        this.setStatus("connecting");
        this.hold();
        if (this.holdTimer) clearTimeout(this.holdTimer);
        const wait = retryAfterMs + Math.floor((this.opts.jitter ?? defaultJitter)());
        this.holdTimer = setTimeout(() => this.release(), wait);
        return;
      }
      case "token_expired":
      case "unauthorized":
        // The token we hold is dead. Reconnecting with it would only be refused
        // again, so wait quietly until a fresh one arrives (onToken).
        this.setStatus("waiting-token");
        this.hold();
        return;
      case "revoked":
        this.opts.cursor.clear();
        this.setStatus("revoked");
        this.hold();
        return;
      default:
        // "limit", or a reason from a newer server: the shared hook's backoff decides.
        this.setStatus("connecting");
    }
  }

  /** A fresh access token: sent in band, or, if we were waiting for one, the wait ends. */
  onToken(token: string): void {
    if (this.current === "revoked") return;
    if (this.current === "waiting-token") {
      this.setStatus("connecting");
      this.release(); // the hook reopens and onOpen sends Auth with this token
      return;
    }
    this.transport?.send({type: "reauth", token});
  }

  /** Stops the timers. The owner calls it when the provider unmounts. */
  dispose(): void {
    if (this.holdTimer) clearTimeout(this.holdTimer);
    this.holdTimer = null;
  }
}

function defaultJitter(): number {
  return Math.random() * MAX_JITTER_MS;
}

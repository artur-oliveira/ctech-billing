"use client";

import {useWebSocket} from "@aoctech/ws-client";
import {useQueryClient} from "@tanstack/react-query";
import {type ReactNode, useEffect, useLayoutEffect, useRef, useState} from "react";

import {API_BASE_URL, getAccessToken, subscribeAccessToken} from "@/lib/api/client";
import {spaceHeader} from "@/lib/console/space";

import {applyChange, applyGap} from "./binding";
import type {Surface} from "./catalogue";
import {decode, encode, type Inbound, type Outbound} from "./codec";
import {DEFAULT_POLL_MS, RealtimeCore, type RealtimeStatus} from "./core";
import {createCursorStore} from "./cursor";
import {type Shared, sharedRealtimeEnabled, useElection} from "./leader";
import {changesFetcher, startPolling} from "./poller";
import {RealtimeStatusContext} from "./useRealtimeStatus";

/** What the first frame carries; the server decides every scope from it. */
export interface AuthFields {
  token?: string
  payLink?: string
  space?: string
  mode?: "live" | "test"
}

/**
 * Realtime is off unless NEXT_PUBLIC_REALTIME is "true", and then on for the
 * surfaces in NEXT_PUBLIC_REALTIME_SURFACES (a comma list; unset means all, so a
 * staged rollout names its surfaces). A surface that is not listed mounts nothing.
 */
export function realtimeEnabledFor(kind: Surface["kind"]): boolean {
  if (process.env.NEXT_PUBLIC_REALTIME !== "true") return false;
  const list = process.env.NEXT_PUBLIC_REALTIME_SURFACES;
  if (!list) return true;
  return list.split(",").map((s) => s.trim()).includes(kind);
}

/** wss://…/v1.0/ws, no query string: NEXT_PUBLIC_WS_URL (the host the CSP allows) or the API's own host with the scheme mapped. */
export function realtimeUrl(): string {
  const configured = process.env.NEXT_PUBLIC_WS_URL;
  if (configured) return `${configured.replace(/\/$/, "")}/v1.0/ws`;
  const base = API_BASE_URL || (typeof window === "undefined" ? "" : window.location.origin);
  return `${base.replace(/^http/, "ws")}/v1.0/ws`;
}

/** The credential and selectors a surface sends, read at the moment of connecting. */
export function defaultAuth(surface: Surface): AuthFields {
  const token = getAccessToken() ?? "";
  switch (surface.kind) {
    case "console":
      return {token, space: `org:${surface.scope.org}`, mode: surface.scope.mode};
    case "finance":
      return {token, space: spaceHeader(surface.space), mode: surface.mode};
    case "portal":
      return {token};
    case "checkout":
      return {payLink: surface.token};
    default:
      return {};
  }
}

/**
 * What the cursor and the connection belong to. Two principals, two modes or two
 * spaces are two keys, so a cursor is never read by another one.
 */
function cursorKeyOf(surface: Surface, principal: string): string {
  switch (surface.kind) {
    case "console":
      return `console:${principal}:${surface.scope.org}:${surface.scope.mode}`;
    case "finance":
      return `finance:${principal}:${surface.mode}:${spaceHeader(surface.space)}`;
    case "portal":
      return `portal:${principal}`;
    case "checkout":
      return `checkout:${surface.token.slice(-24)}`;
    default:
      return "unknown";
  }
}

interface ProviderProps {
  surface: Surface
  /** Who is signed in (their subject). Null disables the stream and forgets its cursor. */
  principal: string | null
  /** The first frame's fields; defaults to what the surface implies. */
  auth?: (surface: Surface) => AuthFields
  children: ReactNode
}

/**
 * Keeps one authorized connection for a surface and turns what it hears into
 * query invalidations. It renders its children untouched and never blocks them:
 * a screen works exactly as before when the socket cannot connect.
 *
 * The connection is a sibling of the children, keyed by principal, mode and space,
 * so changing any of them closes the socket and starts another without
 * remounting a single screen.
 */
export function RealtimeProvider({surface, principal, auth, children}: ProviderProps) {
  const enabled = realtimeEnabledFor(surface.kind) && principal !== null;
  const cursorKey = enabled ? cursorKeyOf(surface, principal) : null;
  const [status, setStatus] = useState<RealtimeStatus>("connecting");

  // Leaving a principal, a mode or a space for another one, or signing out,
  // forgets where its stream had got to. The cleanup reads the key of the commit
  // that replaced it: a layout effect has already stored it by then, so a plain
  // unmount (the page going away, a reload) sees the same key and keeps the cursor
  // for the resume.
  const latestKey = useRef(cursorKey);
  useLayoutEffect(() => {
    latestKey.current = cursorKey;
  });
  useEffect(() => {
    if (cursorKey === null) return undefined;
    return () => {
      if (latestKey.current !== cursorKey) createCursorStore(cursorKey).clear();
    };
  }, [cursorKey]);

  return (
    <RealtimeStatusContext.Provider value={enabled ? status : "off"}>
      {cursorKey !== null && (
        <Connection key={cursorKey} surface={surface} cursorKey={cursorKey} auth={auth ?? defaultAuth} onStatus={setStatus}/>
      )}
      {children}
    </RealtimeStatusContext.Provider>
  );
}

interface ConnectionProps {
  surface: Surface
  cursorKey: string
  auth: (surface: Surface) => AuthFields
  onStatus: (s: RealtimeStatus) => void
}

/**
 * What the core needs from the socket and the election, which are hooks that run
 * after the core is built. It starts inert and is bound once they exist; their
 * send, reconnect and post are stable for the component's life, so the first
 * binding is the only one that matters.
 */
class LateLink {
  private sendFn: (o: Outbound) => boolean = () => false;
  private reconnectFn: () => void = () => {};
  private postFn: (m: Shared) => void = () => {};

  bind(send: (o: Outbound) => boolean, reconnect: () => void, post: (m: Shared) => void): void {
    this.sendFn = send;
    this.reconnectFn = reconnect;
    this.postFn = post;
  }

  send(o: Outbound): boolean {
    return this.sendFn(o);
  }

  reconnect(): void {
    this.reconnectFn();
  }

  post(m: Shared): void {
    this.postFn(m);
  }
}

/** How long the socket may be down before the screens are also fed by polling. */
const UNREACHABLE_AFTER_MS = 15_000;

function Connection({surface, cursorKey, auth, onStatus}: ConnectionProps) {
  const qc = useQueryClient();
  // The component is keyed by the principal, mode and space, so the first surface
  // and auth it sees are the ones for its whole life.
  const [initial] = useState(() => ({surface, auth}));
  const [held, setHeld] = useState(false);
  const [coreStatus, setCoreStatus] = useState<RealtimeStatus>("connecting");

  // What the core needs from the hooks below, which only exist after it: filled in
  // once they do (their send, reconnect and post are stable for the component's life).
  const [link] = useState(() => new LateLink());

  const [core] = useState(() => {
    const c = new RealtimeCore({
      scopeKind: initial.surface.kind,
      cursor: createCursorStore(cursorKey),
      onChange: (change) => {
        applyChange(qc, initial.surface, change);
        link.post({type: "change", change});
      },
      onGap: (reason) => {
        applyGap(qc, initial.surface);
        link.post({type: "gap", reason});
      },
      onStatus: (s) => {
        setCoreStatus(s);
        onStatus(s);
        link.post({type: "status", status: s});
      },
      onHold: setHeld,
    });
    c.attach({
      send: (o) => link.send(o),
      reconnect: () => link.reconnect(),
      close: () => setHeld(true),
    });
    return c;
  });

  // With one socket per browser (flagged), only the elected tab holds the socket;
  // the others apply what the leader tells them. Alone, an instance is its own leader.
  const election = useElection(cursorKey, sharedRealtimeEnabled(), (shared) => {
    if (shared.type === "change") core.onFrame({type: "change", change: shared.change});
    else if (shared.type === "gap") core.onFrame({type: "gap", reason: shared.reason});
    else onStatus(shared.status);
  });

  const socket = useWebSocket({
    url: realtimeUrl(),
    // Held after a draining Bye (until its retry-after) or while waiting for a
    // token: the shared hook would otherwise reconnect on its own in a second.
    enabled: !held && election.leader && coreStatus !== "off",
    binaryType: "arraybuffer",
    encode,
    decode,
    onMessage: (frame) => core.onFrame(frame as Inbound),
    onOpen: () => core.onOpen(initial.auth(initial.surface)),
    reconnectForever: true,
    reconnectJitter: true,
    recoverOnNetworkRestore: true,
    connectTimeoutMs: 10_000,
  });

  useEffect(() => {
    link.bind(socket.send, socket.reconnect, election.post);
  }, [link, socket.send, socket.reconnect, election.post]);

  useEffect(() => {
    if (socket.status === "disconnected" || socket.status === "error") core.onClose();
  }, [socket.status, core]);

  // The ladder's second rung: a socket that has been down for a while is not
  // coming back soon, so the cursor route feeds the screens until it does. The
  // first rung is the shared hook's own backoff; the last is the screens'
  // refetch on focus, which is what they did before any of this.
  // Keyed on connected-or-not, not on the hook's finer states: a socket that
  // flaps between connecting and disconnected must not restart the count.
  const connected = socket.status === "connected";
  useEffect(() => {
    if (connected || !election.leader) return undefined;
    const timer = setTimeout(() => core.onUnreachable(), UNREACHABLE_AFTER_MS);
    return () => clearTimeout(timer);
  }, [connected, election.leader, core]);

  useEffect(() => {
    if (coreStatus !== "polling" || !election.leader) return undefined;
    return startPolling({
      fetchChanges: changesFetcher(initial.surface),
      core,
      intervalMs: core.pollIntervalMs || DEFAULT_POLL_MS,
      visible: () => typeof document === "undefined" || document.visibilityState === "visible",
    });
  }, [coreStatus, election.leader, core, initial.surface]);

  useEffect(() => {
    // A checkout link is not renewed: the page reconnects with the same link.
    if (initial.surface.kind === "checkout") return undefined;
    return subscribeAccessToken((token) => core.onToken(token));
  }, [core, initial.surface.kind]);

  useEffect(() => () => core.dispose(), [core]);

  return null;
}

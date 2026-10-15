"use client";

import {useWebSocket} from "@aoctech/ws-client";
import {useQueryClient} from "@tanstack/react-query";
import {type ReactNode, useEffect, useLayoutEffect, useRef, useState} from "react";

import {API_BASE_URL, getAccessToken, subscribeAccessToken} from "@/lib/api/client";
import {spaceHeader} from "@/lib/console/space";

import {applyChange, applyGap} from "./binding";
import type {Surface} from "./catalogue";
import {decode, encode, type Inbound} from "./codec";
import {RealtimeCore, type RealtimeStatus} from "./core";
import {createCursorStore} from "./cursor";
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

/** wss://…/v1.0/ws: the API's own host with the scheme mapped, and no query string. */
export function realtimeUrl(): string {
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
  const [status, setStatus] = useState<RealtimeStatus>("off");

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

function Connection({surface, cursorKey, auth, onStatus}: ConnectionProps) {
  const qc = useQueryClient();
  // The component is keyed by the principal, mode and space, so the first surface
  // and auth it sees are the ones for its whole life.
  const [initial] = useState(() => ({surface, auth}));
  const [held, setHeld] = useState(false);

  const socket = useWebSocket({
    url: realtimeUrl(),
    // Held after a draining Bye (until its retry-after) or while waiting for a
    // token: the shared hook would otherwise reconnect on its own in a second.
    enabled: !held,
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

  // Built after the hook, whose send and reconnect are stable for its lifetime.
  const [core] = useState(() => {
    const c = new RealtimeCore({
      scopeKind: initial.surface.kind,
      cursor: createCursorStore(cursorKey),
      onChange: (change) => applyChange(qc, initial.surface, change),
      onGap: () => applyGap(qc, initial.surface),
      onStatus,
      onHold: setHeld,
    });
    c.attach({
      send: (o) => socket.send(o),
      reconnect: () => socket.reconnect(),
      close: () => setHeld(true),
    });
    return c;
  });

  useEffect(() => {
    if (socket.status === "disconnected" || socket.status === "error") core.onClose();
  }, [socket.status, core]);

  useEffect(() => {
    // A checkout link is not renewed: the page reconnects with the same link.
    if (initial.surface.kind === "checkout") return undefined;
    return subscribeAccessToken((token) => core.onToken(token));
  }, [core, initial.surface.kind]);

  useEffect(() => () => core.dispose(), [core]);

  return null;
}

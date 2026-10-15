"use client";

import {useCallback, useEffect, useRef, useState} from "react";

import type {ChangeView} from "./codec";
import type {RealtimeStatus} from "./core";

/** What the leader tells the other tabs. Never a credential, never raw frames. */
export type Shared =
  | {type: "change"; change: ChangeView}
  | {type: "gap"; reason: string}
  | {type: "status"; status: RealtimeStatus}

/** The part of navigator.locks the election needs. */
export interface LockApi {
  request(name: string, callback: () => Promise<unknown>): Promise<unknown>
}

export interface ChannelLike {
  postMessage(data: unknown): void
  onmessage: ((event: {data: unknown}) => void) | null
  close(): void
}

export interface LeaderEnv {
  locks: LockApi | null
  makeChannel: ((name: string) => ChannelLike) | null
}

/** One socket per browser is opt-in until the soak says otherwise. */
export function sharedRealtimeEnabled(): boolean {
  return process.env.NEXT_PUBLIC_REALTIME_SHARED === "true";
}

/** What this browser offers, or nothing: every tab then connects on its own. */
export function browserEnv(): LeaderEnv {
  try {
    const locks = typeof navigator !== "undefined" && "locks" in navigator ? (navigator.locks as unknown as LockApi) : null;
    const makeChannel = typeof BroadcastChannel === "undefined" ? null : (name: string) => new BroadcastChannel(name) as unknown as ChannelLike;
    return {locks, makeChannel};
  } catch {
    return {locks: null, makeChannel: null};
  }
}

/**
 * The lock and channel name for a principal. The cursor key already names the
 * principal, the mode and the space; it is hashed so the name carries none of
 * them, and two principals never share a channel.
 */
export function channelName(cursorKey: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < cursorKey.length; i++) {
    hash ^= cursorKey.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return `billing-rt:${hash.toString(16).padStart(8, "0")}`;
}

export interface Election {
  /** True when this instance should hold the socket. */
  leader: boolean
  /** Tells the other tabs; a no-op for a follower or when running alone. */
  post: (message: Shared) => void
}

/**
 * Elects one instance per principal to hold the socket, with a Web Lock named for
 * the principal: the others queue on it, so when the leader goes (it unmounts or
 * its tab closes, which releases the lock by itself) exactly one of them is
 * granted it and takes over from its own stored cursor. The leader's changes reach
 * the others over a BroadcastChannel of the same name. Without both APIs, or with
 * the flag off, an instance is simply its own leader.
 */
export function useElection(
  cursorKey: string,
  enabled: boolean,
  onMessage: (message: Shared) => void,
  env: LeaderEnv = browserEnv(),
): Election {
  const available = enabled && env.locks !== null && env.makeChannel !== null;
  const [leader, setLeader] = useState(!available);
  const leaderRef = useRef(!available);
  const channelRef = useRef<ChannelLike | null>(null);
  const handler = useRef(onMessage);

  useEffect(() => {
    handler.current = onMessage;
  });

  useEffect(() => {
    if (!available || !env.locks || !env.makeChannel) return undefined;
    const name = channelName(cursorKey);
    const channel = env.makeChannel(name);
    channelRef.current = channel;
    channel.onmessage = (event) => handler.current(event.data as Shared);

    let release: () => void = () => {};
    let left = false;
    void env.locks.request(name, () => {
      if (left) return Promise.resolve();
      leaderRef.current = true;
      setLeader(true);
      return new Promise<void>((resolve) => {
        release = resolve;
      });
    });
    return () => {
      left = true;
      leaderRef.current = false;
      release();
      channel.close();
      channelRef.current = null;
      setLeader(false);
    };
    // env is read once per mount: a different browser environment is a different mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cursorKey, available]);

  const post = useCallback((message: Shared) => {
    if (leaderRef.current) channelRef.current?.postMessage(message);
  }, []);

  return {leader, post};
}

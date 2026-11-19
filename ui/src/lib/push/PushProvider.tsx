"use client";

import {createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode} from "react";
import {useRouter} from "next/navigation";
import {onNavigateMessage, registerServiceWorker, usePushSubscription, type UsePushSubscriptionResult} from "@aoctech/push-client";

import {useSubject} from "@/lib/auth/subject";
import {pushCallbacks} from "@/lib/push/callbacks";
import {PUSH_STAMP_KEY, PUSH_USER_KEY} from "@/lib/push/storage";

/** Chromium's install prompt event; not in the DOM lib types. */
interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>
}

export interface PushContextValue extends UsePushSubscriptionResult {
  /** The browser offered to install the app (Chromium); iPhone never fires this, see the install steps instead. */
  canInstall: boolean
  install: () => Promise<void>
}

const PushContext = createContext<PushContextValue | null>(null);

/** Push status and actions for this device; the single instance lives in `PushProvider`. */
export function usePush(): PushContextValue {
  const value = useContext(PushContext);
  if (!value) throw new Error("usePush precisa estar dentro do PushProvider");
  return value;
}

/**
 * Registers the push service worker, keeps this device's subscription fresh once someone is signed in (`syncOnLoad`: it
 * never prompts) and routes notification clicks. Same provider as Poker's and the DF-e's, without their app-specific
 * parts.
 */
export function PushProvider({children}: {children: ReactNode}) {
  const router = useRouter();
  const subject = useSubject();

  useEffect(() => {
    void registerServiceWorker();
  }, []);

  useEffect(() => onNavigateMessage(path => router.push(path)), [router]);

  // One browser, one person. The sync stamp is per browser, so a different person signing in on a shared browser would be
  // told "synced 5 minutes ago" and the subscription would stay on the previous person's row. Dropping the stamp when the
  // signed-in person changes makes `syncOnLoad` resend it, and the API moves the endpoint to the current person.
  // Declared before `usePushSubscription` on purpose: effects run in order, so this one runs before the sync.
  useEffect(() => {
    if (!subject) return;
    try {
      if (localStorage.getItem(PUSH_USER_KEY) !== subject) {
        localStorage.removeItem(PUSH_STAMP_KEY);
        localStorage.setItem(PUSH_USER_KEY, subject);
      }
    } catch {
      // Storage unavailable: the stamp's own 24 h expiry still resends the subscription.
    }
  }, [subject]);

  const push = usePushSubscription({...pushCallbacks, enabled: !!subject});

  // `beforeinstallprompt` fires once, early, usually before the notifications screen exists: keep it here.
  const [installEvent, setInstallEvent] = useState<BeforeInstallPromptEvent | null>(null);
  useEffect(() => {
    const onPrompt = (e: Event) => {
      e.preventDefault();
      setInstallEvent(e as BeforeInstallPromptEvent);
    };
    const onInstalled = () => setInstallEvent(null);
    window.addEventListener("beforeinstallprompt", onPrompt);
    window.addEventListener("appinstalled", onInstalled);
    return () => {
      window.removeEventListener("beforeinstallprompt", onPrompt);
      window.removeEventListener("appinstalled", onInstalled);
    };
  }, []);
  const install = useCallback(async () => {
    if (!installEvent) return;
    await installEvent.prompt();
    setInstallEvent(null);
  }, [installEvent]);

  const value = useMemo<PushContextValue>(
    () => ({...push, canInstall: installEvent !== null, install}),
    [push, installEvent, install],
  );

  return <PushContext.Provider value={value}>{children}</PushContext.Provider>;
}

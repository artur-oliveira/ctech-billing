"use client";

import {useEffect, useState} from "react";
import {useTranslation} from "react-i18next";

import {DEFAULT_POLL_MS} from "@/lib/realtime/core";
import {useRealtimeStatus} from "@/lib/realtime/useRealtimeStatus";

/** A connection that settles within this is not worth announcing. */
const QUIET_MS = 3_000;

/**
 * Whether the page is following changes by itself. Said only when it is true
 * ("Ao vivo") or when it is the slower fallback ("Atualizando a cada 10 s");
 * never while connecting for the first moments, never when realtime is off.
 */
export function LiveIndicator() {
  const {t} = useTranslation();
  const status = useRealtimeStatus();
  const [patient, setPatient] = useState(false);
  const waiting = status === "connecting" || status === "waiting-token";

  useEffect(() => {
    if (!waiting) return undefined;
    const timer = setTimeout(() => setPatient(true), QUIET_MS);
    return () => {
      clearTimeout(timer);
      setPatient(false);
    };
  }, [waiting]);

  let text: string | null = null;
  if (status === "live") text = t("common.realtime.live");
  else if (status === "polling" || (waiting && patient)) text = t("common.realtime.polling", {seconds: DEFAULT_POLL_MS / 1000});

  return (
    <span role="status" aria-live="polite" className="text-xs text-muted-foreground">
      {text}
    </span>
  );
}

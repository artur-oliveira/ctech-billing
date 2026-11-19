"use client";

import {Button, Field, PageHeader, Select, Skeleton, Switch} from "@aoctech/ui";
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {getPushConfig, getPushPreferences, putPushPreferences, pushKeys, sendPushTest, type PushPreferences} from "@/lib/api/push";
import {usePush} from "@/lib/push/PushProvider";
import {selectCopy} from "@/lib/selectCopy";

/**
 * The server places reminders in 15-minute slots of the day (go-common pushsvc: 96, "00:00" to "23:45"), so the time is a
 * choice among exactly those, never a free-typed value the server would round down.
 */
const SLOT_OPTIONS = Array.from({length: 96}, (_, i) => {
  const label = `${String(Math.floor(i / 4)).padStart(2, "0")}:${String((i % 4) * 15).padStart(2, "0")}`;
  return {value: label, label};
});

/**
 * Web Push in Finanças: turn notifications on for this device, then choose the daily reminder of items waiting for the
 * person's confirmation (docs/specs/2026-11-18-pending-reminder-web-push.md). Permission is asked only on the button click,
 * never on load. The reminder starts off; the person picks the time.
 */
export function NotificationsView() {
  const {t} = useTranslation();
  const push = usePush();
  const queryClient = useQueryClient();
  const config = useQuery({queryKey: pushKeys.config, queryFn: getPushConfig});
  const available = config.data?.enabled === true;
  const subscribed = push.status === "subscribed";
  const prefs = useQuery({queryKey: pushKeys.preferences, queryFn: getPushPreferences, enabled: available && subscribed});

  const activate = useMutation({
    mutationFn: () => push.subscribe(),
    onSuccess: () => queryClient.invalidateQueries({queryKey: pushKeys.preferences}),
    onError: () => toast.error(t("finance.notifications.activateError")),
  });
  const deactivate = useMutation({
    mutationFn: () => push.unsubscribe(),
    onError: () => toast.error(t("finance.notifications.deactivateError")),
  });
  const test = useMutation({
    mutationFn: () => sendPushTest(),
    onSuccess: () => toast.success(t("finance.notifications.testSent")),
    onError: () => toast.error(t("finance.notifications.testError")),
  });
  const busy = activate.isPending || deactivate.isPending || test.isPending;

  return (
    <div className="max-w-3xl space-y-4">
      <PageHeader title={t("finance.notifications.title")} description={t("finance.notifications.description")}/>
      {config.isPending ? (
        <Skeleton className="h-28" aria-label={t("finance.notifications.loading")}/>
      ) : config.isError || !config.data ? (
        <div role="alert" className="flex flex-wrap items-center gap-3 text-sm">
          {t("finance.notifications.loadError")}
          <Button variant="outline" size="sm" onClick={() => void config.refetch()}>{t("finance.notifications.retry")}</Button>
        </div>
      ) : !available ? (
        <p className="text-sm text-muted-foreground">{t("finance.notifications.unavailable")}</p>
      ) : (
        <>
          <DeviceState status={push.status} canInstall={push.canInstall} busy={busy}
                       onActivate={() => activate.mutate()} onInstall={() => void push.install()}/>
          {subscribed && (
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" disabled={busy} onClick={() => test.mutate()}>{t("finance.notifications.test")}</Button>
              <Button variant="outline" disabled={busy} onClick={() => deactivate.mutate()}>{t("finance.notifications.deactivate")}</Button>
            </div>
          )}
          {subscribed && (prefs.isPending ? (
            <Skeleton className="h-24" aria-label={t("finance.notifications.loadingPrefs")}/>
          ) : prefs.isError || !prefs.data ? (
            <div role="alert" className="flex flex-wrap items-center gap-3 text-sm">
              {t("finance.notifications.prefsError")}
              <Button variant="outline" size="sm" onClick={() => void prefs.refetch()}>{t("finance.notifications.retry")}</Button>
            </div>
          ) : (
            // Keyed on the loaded values so a fresh save remounts the form with the server state.
            <ReminderForm key={`${prefs.data.pending_reminder.enabled}:${prefs.data.pending_reminder.time}`} prefs={prefs.data}/>
          ))}
        </>
      )}
    </div>
  );
}

function DeviceState({status, canInstall, busy, onActivate, onInstall}: {
  status: string
  canInstall: boolean
  busy: boolean
  onActivate: () => void
  onInstall: () => void
}) {
  const {t} = useTranslation();
  switch (status) {
    case "loading": return <p className="text-sm text-muted-foreground">{t("finance.notifications.checking")}</p>;
    case "unsupported": return <p className="text-sm text-muted-foreground">{t("finance.notifications.unsupported")}</p>;
    case "needs-install": return <p className="text-sm text-muted-foreground">{t("finance.notifications.needsInstall")}</p>;
    case "denied": return <p className="text-sm text-muted-foreground">{t("finance.notifications.denied")}</p>;
    case "subscribed": return <p className="text-sm" role="status">{t("finance.notifications.subscribed")}</p>;
    default:
      return (
        <div className="space-y-2">
          {status === "error" && <p role="alert" className="text-sm">{t("finance.notifications.error")}</p>}
          <div className="flex flex-wrap items-center gap-3">
            <Button disabled={busy} onClick={onActivate}>{t("finance.notifications.activate")}</Button>
            <small className="text-muted-foreground">{t("finance.notifications.activateHint")}</small>
            {canInstall && <Button variant="outline" onClick={onInstall}>{t("finance.notifications.install")}</Button>}
          </div>
        </div>
      );
  }
}

function ReminderForm({prefs}: {prefs: PushPreferences}) {
  const {t} = useTranslation();
  const queryClient = useQueryClient();
  const [on, setOn] = useState(prefs.pending_reminder.enabled);
  const [time, setTime] = useState(prefs.pending_reminder.time);
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: (p: PushPreferences) => putPushPreferences(p),
    onSuccess: saved => {
      queryClient.setQueryData(pushKeys.preferences, saved);
      toast.success(t("finance.notifications.saved"));
    },
    onError: () => toast.error(t("finance.notifications.saveError")),
  });

  return (
    <form className="space-y-3" onSubmit={event => {
      event.preventDefault();
      if (on && !time) {
        setError(t("finance.notifications.timeRequired"));
        return;
      }
      setError(null);
      save.mutate({pending_reminder: {enabled: on, time}});
    }}>
      <div className="flex items-start justify-between gap-4">
        <span>
          <b className="block text-sm">{t("finance.notifications.pendingLabel")}</b>
          <small className="text-muted-foreground">{t("finance.notifications.pendingHint")}</small>
        </span>
        <Switch checked={on} onCheckedChange={setOn} aria-label={t("finance.notifications.pendingLabel")}/>
      </div>
      {on && (
        <Field label={t("finance.notifications.timeLabel")} hint={t("finance.notifications.timeHint")}>
          {control => <Select {...selectCopy()} {...control} value={time} onValueChange={setTime} options={SLOT_OPTIONS}/>}
        </Field>
      )}
      {error && <p role="alert" className="text-sm">{error}</p>}
      <Button type="submit" disabled={save.isPending}>{save.isPending ? t("finance.notifications.saving") : t("finance.notifications.save")}</Button>
    </form>
  );
}

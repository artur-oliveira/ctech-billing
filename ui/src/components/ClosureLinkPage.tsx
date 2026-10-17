"use client";

import {Alert, Button} from "@aoctech/ui";
import {useMutation} from "@tanstack/react-query";
import {useTranslation} from "react-i18next";

import {StatusScreen} from "@/components/StatusScreen";
import {messageFor, problemCode} from "@/lib/api/client";
import type {Closure} from "@/lib/api/closure";
import {instantDate} from "@/lib/format";

type Props = {
  kind: "confirm" | "cancel"
  requestID: string
  token: string
  action: (requestID: string, token: string) => Promise<Closure>
}

/**
 * The page a closure e-mail link opens. It acts only when the button is pressed
 * (a POST): a mail scanner that prefetches links must never confirm or cancel a
 * closure.
 */
export function ClosureLinkPage({kind, requestID, token, action}: Props) {
  const {t} = useTranslation();
  const run = useMutation({mutationFn: () => action(requestID, token)});
  const missing = !requestID || !token;
  const code = problemCode(run.error);
  const error = run.isError
    ? code === "invalid_link" ? t("closure.link.invalid")
      : code === "closure_state" ? t("closure.link.tooLate")
        : messageFor(run.error)
    : "";

  const body = run.isSuccess ? (
    <Alert tone="success" title={t(`closure.link.${kind}.title`)}>
      {t(`closure.link.${kind}.done`, {date: run.data.grace_until ? instantDate(run.data.grace_until) : ""})}
    </Alert>
  ) : missing ? (
    <Alert tone="danger" title={t(`closure.link.${kind}.title`)}>{t("closure.link.invalid")}</Alert>
  ) : (
    <>
      {error && <Alert tone="danger" title={t(`closure.link.${kind}.title`)}>{error}</Alert>}
      <Button variant={kind === "confirm" ? "danger" : "brand"} disabled={run.isPending} onClick={() => run.mutate()}>
        {t(`closure.link.${kind}.button`)}
      </Button>
    </>
  );

  return (
    <StatusScreen
      title={t(`closure.link.${kind}.title`)}
      description={t(`closure.link.${kind}.description`)}
      action={body}
    />
  );
}

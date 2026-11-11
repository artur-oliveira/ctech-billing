"use client";

import {Button, Modal, Skeleton} from "@aoctech/ui";
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query";
import {ArrowLeft} from "lucide-react";
import Link from "next/link";
import {useSearchParams} from "next/navigation";
import {Suspense, useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {DunningPolicyCard} from "@/components/console/DunningPolicyCard";
import {NewPriceDialog} from "@/components/console/NewPriceDialog";
import {ProductDetailsDialog} from "@/components/console/ProductDetailsDialog";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {CopyableValue} from "@/components/ui/CopyableValue";
import {Section} from "@/components/ui/Section";
import {messageFor, statusOf} from "@/lib/api/client";
import {
  archivePrice,
  consoleKeys,
  getConsoleProduct,
  setProductDunningPolicy,
} from "@/lib/api/console";
import type {ConsolePrice, DunningStep} from "@/lib/api/consoleTypes";
import {useConsoleScope} from "@/lib/console/scope";
import {money} from "@/lib/format";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";
import {NARROW, useMediaQuery} from "@/lib/hooks/useMediaQuery";

/**
 * C9 — a product and its prices.
 *
 * The action is **"novo preço", never "editar preço"**, and that is the whole
 * design of this screen. A price is immutable: subscriptions pin the one they
 * were created on and keep billing at it forever, which is what makes
 * grandfathering a consequence of the model rather than a flag somebody has to
 * remember. An edit button here would create a new price behind the operator's
 * back and leave them believing they had changed what existing customers pay.
 *
 * Archived prices stay listed for the same reason: a subscription may still be
 * on one, and hiding it makes the invoice it produces look like it came from
 * nowhere.
 */
export default function ConsoleProductPage() {
  return (
    <Suspense fallback={<DetailSkeleton/>}>
      <Detail/>
    </Suspense>
  );
}

function Detail() {
  const {t} = useTranslation();
  const id = useSearchParams().get("id") ?? "";
  const scope = useConsoleScope();
  const queryClient = useQueryClient();
  const [pricing, setPricing] = useState(false);
  const [editing, setEditing] = useState(false);
  const [archiving, setArchiving] = useState<ConsolePrice | null>(null);

  const query = useQuery({
    queryKey: consoleKeys.product(scope, id),
    queryFn: () => getConsoleProduct(scope, id),
    enabled: id !== "",
  });

  const refresh = () => {
    void queryClient.invalidateQueries({queryKey: consoleKeys.product(scope, id)});
    void queryClient.invalidateQueries({queryKey: consoleKeys.products(scope)});
  };

  const savePolicy = useMutation({
    mutationFn: (steps: DunningStep[]) => setProductDunningPolicy(scope, id, steps),
    onSuccess: refresh,
  });

  const archive = useMutation({
    mutationFn: (priceId: string) => archivePrice(scope, priceId),
    onSuccess: () => {
      toast.success(t("console.product.archivedDone"));
      refresh();
    },
    onError: error => toast.error(messageFor(error)),
    onSettled: () => setArchiving(null),
  });

  useDocumentTitle(query.data?.name ?? null);

  if (id === "" || (query.isError && statusOf(query.error) === 404)) return <NotFound/>;
  if (query.isPending) return <DetailSkeleton/>;
  if (query.isError) {
    return (
      <div className="space-y-6">
        <BackLink/>
        <ErrorBlock error={query.error} onRetry={query.refetch}/>
      </div>
    );
  }

  const product = query.data;
  const prices = product.prices ?? [];

  return (
    <div className="space-y-8">
      <header className="space-y-3">
        <BackLink/>
        <div className="flex flex-wrap items-center justify-between gap-4">
          <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
            {product.name}
          </h1>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={() => setEditing(true)}>{t("console.product.editDetails")}</Button>
            <Button size="sm" onClick={() => setPricing(true)}>{t("console.product.newPrice")}</Button>
          </div>
        </div>
        {(product.description || product.url) && (
          <div className="space-y-1 text-sm text-muted-foreground">
            {product.description && <p className="max-w-prose">{product.description}</p>}
            {product.url && (
              <a href={product.url} target="_blank" rel="noopener noreferrer"
                 className="text-brand-600 underline-offset-4 hover:underline">
                {product.url}
              </a>
            )}
          </div>
        )}
        {/* Said once, on the screen where it matters, rather than in a tooltip
            nobody opens. */}
        <p className="max-w-prose text-sm text-muted-foreground">
          {t("console.product.note")}
        </p>
      </header>

      <Section title={t("console.product.prices")} tone="muted">
        {prices.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {t("console.product.empty")}
          </p>
        ) : (
          <PriceList prices={prices} onArchive={setArchiving}/>
        )}
      </Section>

      {product.dunning && (
        <DunningPolicyCard
          title={t("console.product.dunningTitle")}
          description={t("console.product.dunningDescription")}
          policy={product.dunning}
          inheritLabel={t("console.product.inherit")}
          onSave={steps => savePolicy.mutateAsync(steps)}
        />
      )}

      <ProductDetailsDialog
        key={`${product.description ?? ""}|${product.url ?? ""}`}
        open={editing}
        onClose={() => setEditing(false)}
        productId={product.id}
        description={product.description ?? ""}
        url={product.url ?? ""}
        onSaved={refresh}
      />

      <NewPriceDialog
        open={pricing}
        productId={product.id}
        onClose={() => setPricing(false)}
        onCreated={() => {
          refresh();
          setPricing(false);
        }}
      />

      <Modal
        open={archiving !== null}
        onClose={() => setArchiving(null)}
        title={t("console.product.archiveTitle")}
        description={t("console.product.archiveBody")}
        cancelLabel={t("console.product.keep")}
        submitLabel={t("console.product.archiveConfirm")}
        loading={archive.isPending}
        onSubmit={() => archiving && archive.mutate(archiving.id)}
      />
    </div>
  );
}

/** Price versions: a table from `sm`, a list on a phone (DESIGN.md "On a phone"). */
function PriceList({prices, onArchive}: {prices: ConsolePrice[]; onArchive: (price: ConsolePrice) => void}) {
  const {t} = useTranslation();
  const phone = useMediaQuery(NARROW);
  const recurrence = (price: ConsolePrice) =>
    (price.recurrence.count > 1
      ? t(`console.every.${price.recurrence.interval}`, {count: price.recurrence.count, defaultValue: price.recurrence.interval})
      : t(`console.interval.${price.recurrence.interval}`, {defaultValue: price.recurrence.interval}))
    + ` · ${price.billing_timing === "arrears" ? t("console.timing.shortArrears") : t("console.timing.shortAdvance")}`;
  const kind = (price: ConsolePrice) => price.type === "metered" ? t("console.priceType.metered") : t("console.priceType.fixed");
  const amount = (price: ConsolePrice) => (
    <>
      {money(price.unit_amount, price.currency)}
      {price.type === "metered" && <span className="text-muted-foreground"> {t("console.priceType.perUnit")}</span>}
    </>
  );
  const archive = (price: ConsolePrice, className?: string) => !price.archived && (
    <Button variant="outline" size="sm" className={className} onClick={() => onArchive(price)}
      aria-label={t("console.product.archiveNamed", {name: price.name || price.id})}>
      {t("console.product.archive")}
    </Button>
  );
  if (phone) return (
    <ul aria-label={t("console.product.prices")} className="divide-y divide-border border-y border-border">
      {prices.map(price => (
        <li key={price.id} className="space-y-1 py-3">
          <div className="flex items-start justify-between gap-3">
            <p className="min-w-0 text-sm text-foreground [overflow-wrap:anywhere]">{price.name || price.id}</p>
            <span data-numeric className="shrink-0 text-sm text-foreground">{amount(price)}</span>
          </div>
          {price.name && <CopyableValue value={price.id}/>}
          <p className="text-xs text-muted-foreground">
            {kind(price)} · {recurrence(price)}
            {price.archived && ` · ${t("console.product.archived")}`}
          </p>
          {archive(price, "mt-1 max-sm:min-h-11")}
        </li>
      ))}
    </ul>
  );
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[40rem] border-collapse text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs text-muted-foreground">
            <th scope="col" className="py-2 pr-4 font-medium">{t("console.product.price")}</th>
            <th scope="col" className="py-2 pr-4 font-medium">{t("console.product.type")}</th>
            <th scope="col" className="py-2 pr-4 font-medium">{t("console.product.recurrence")}</th>
            <th scope="col" className="py-2 pr-4 text-right font-medium">{t("console.product.amount")}</th>
            <th scope="col" className="py-2 text-right font-medium"><span className="sr-only">{t("console.product.archive")}</span></th>
          </tr>
        </thead>
        <tbody>
          {prices.map(price => (
            <tr key={price.id} className="border-b border-border last:border-0">
              <td className="py-2 pr-4 text-muted-foreground">
                {price.name ? <span className="text-foreground">{price.name}</span> : price.id}
                {price.name && <CopyableValue value={price.id}/>}
                {price.archived && (
                  <span className="ml-2 text-xs text-muted-foreground">{t("console.product.archived")}</span>
                )}
              </td>
              <td className="py-2 pr-4 text-muted-foreground">{kind(price)}</td>
              <td className="py-2 pr-4 text-muted-foreground">{recurrence(price)}</td>
              <td data-numeric className="py-2 pr-4 text-right text-foreground">{amount(price)}</td>
              <td className="py-2 text-right">{archive(price)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function NotFound() {
  const {t} = useTranslation();
  return (
    <div className="space-y-6">
      <BackLink/>
      <div className="space-y-2">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
          {t("console.product.notFound")}
        </h1>
        <p className="text-sm text-muted-foreground">
          {t("console.wrongMode")}
        </p>
      </div>
    </div>
  );
}

function BackLink() {
  const {t} = useTranslation();
  return (
    <Link
      href="/console/catalog"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      {t("console.product.back")}
    </Link>
  );
}

function DetailSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-6 w-48"/>
      <Skeleton className="h-24 w-full"/>
    </div>
  );
}

"use client"

import {Button, Field, Input, Skeleton} from "@aoctech/ui"
import {Modal} from "@/components/ui/ConsoleOverlay"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ArrowLeft} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {DunningPolicyCard} from "@/components/console/DunningPolicyCard"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor, statusOf} from "@/lib/api/client"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {
  archivePrice,
  consoleKeys,
  createPrice,
  getConsoleProduct,
  setProductDunningPolicy,
} from "@/lib/api/console"
import type {ConsolePrice, DunningStep} from "@/lib/api/consoleTypes"
import {useMode} from "@/lib/console/useMode"
import {money} from "@/lib/format"
import {maskMoney, moneyPlaceholder, parseMoney} from "@/lib/money"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

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
  )
}

function Detail() {
  const {t} = useTranslation()
  const id = useSearchParams().get("id") ?? ""
  const mode = useMode()
  const queryClient = useQueryClient()
  const [pricing, setPricing] = useState(false)
  const [archiving, setArchiving] = useState<ConsolePrice | null>(null)

  const query = useQuery({
    queryKey: consoleKeys.product(mode, id),
    queryFn: () => getConsoleProduct(id, mode),
    enabled: id !== "",
  })

  const refresh = () => {
    void queryClient.invalidateQueries({queryKey: consoleKeys.product(mode, id)})
    void queryClient.invalidateQueries({queryKey: consoleKeys.products(mode)})
  }

  const savePolicy = useMutation({
    mutationFn: (steps: DunningStep[]) => setProductDunningPolicy(id, steps, mode),
    onSuccess: refresh,
  })

  const archive = useMutation({
    mutationFn: (priceId: string) => archivePrice(priceId, mode),
    onSuccess: () => {
      toast.success(t("console.product.archivedDone"))
      refresh()
    },
    onError: error => toast.error(messageFor(error)),
    onSettled: () => setArchiving(null),
  })

  useDocumentTitle(query.data?.name ?? null)

  if (id === "" || (query.isError && statusOf(query.error) === 404)) return <NotFound/>
  if (query.isPending) return <DetailSkeleton/>
  if (query.isError) {
    return (
      <div className="space-y-6">
        <BackLink/>
        <ErrorBlock error={query.error} onRetry={query.refetch}/>
      </div>
    )
  }

  const product = query.data
  const prices = product.prices ?? []

  return (
    <div className="space-y-8">
      <header className="space-y-3">
        <BackLink/>
        <div className="flex flex-wrap items-center justify-between gap-4">
          <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
            {product.name}
          </h1>
          <Button size="sm" onClick={() => setPricing(true)}>{t("console.product.newPrice")}</Button>
        </div>
        {/* Said once, on the screen where it matters, rather than in a tooltip
            nobody opens. */}
        <p className="max-w-prose text-sm text-muted-foreground">
          {t("console.product.note")}
        </p>
      </header>

      <section aria-labelledby="precos" className="space-y-3">
        <h2 id="precos" className="text-sm font-medium text-muted-foreground">{t("console.product.prices")}</h2>
        {prices.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {t("console.product.empty")}
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[40rem] border-collapse text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th scope="col" className="py-2 pr-4 font-medium">{t("console.product.price")}</th>
                  <th scope="col" className="py-2 pr-4 font-medium">{t("console.product.type")}</th>
                  <th scope="col" className="py-2 pr-4 font-medium">{t("console.product.recurrence")}</th>
                  <th scope="col" className="py-2 pr-4 text-right font-medium">{t("console.product.amount")}</th>
                  <th scope="col" className="py-2 text-right font-medium"/>
                </tr>
              </thead>
              <tbody>
                {prices.map(price => (
                  <tr
                    key={price.id}
                    className="border-b border-border last:border-0"
                  >
                    <td className="py-2 pr-4 text-muted-foreground">
                      {price.id}
                      {price.archived && (
                        <span className="ml-2 text-xs text-muted-foreground">{t("console.product.archived")}</span>
                      )}
                    </td>
                    <td className="py-2 pr-4 text-muted-foreground">
                      {price.type === "metered" ? t("console.priceType.metered") : t("console.priceType.fixed")}
                    </td>
                    <td className="py-2 pr-4 text-muted-foreground">
                      {t(`console.interval.${price.recurrence.interval}`, {defaultValue: price.recurrence.interval})}
                      {` · ${price.billing_timing === "arrears" ? t("console.timing.shortArrears") : t("console.timing.shortAdvance")}`}
                    </td>
                    <td data-numeric className="py-2 pr-4 text-right text-foreground">
                      {money(price.unit_amount, price.currency)}
                      {price.type === "metered" && (
                        <span className="text-muted-foreground"> {t("console.priceType.perUnit")}</span>
                      )}
                    </td>
                    <td className="py-2 text-right">
                      {!price.archived && (
                        <Button variant="outline" size="sm" onClick={() => setArchiving(price)}>
                          {t("console.product.archive")}
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {product.dunning && (
        <DunningPolicyCard
          title={t("console.product.dunningTitle")}
          description={t("console.product.dunningDescription")}
          policy={product.dunning}
          inheritLabel={t("console.product.inherit")}
          onSave={steps => savePolicy.mutateAsync(steps)}
        />
      )}

      <NewPriceDialog
        open={pricing}
        productId={product.id}
        onClose={() => setPricing(false)}
        onCreated={() => {
          refresh()
          setPricing(false)
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
  )
}

function NewPriceDialog({
  open,
  productId,
  onClose,
  onCreated,
}: { open: boolean; productId: string; onClose: () => void; onCreated: () => void }) {
  const {t} = useTranslation()
  const mode = useMode()
  const [amount, setAmount] = useState("")
  const [type, setType] = useState<"fixed" | "metered">("fixed")

  // A price may be zero (free); anything else goes through the same locale-aware parse
  // as every money field, never through a float.
  const cents = /^0+([.,]0{0,2})?$/.test(amount.trim()) ? 0 : (parseMoney(amount) ?? -1)
  const valid = cents >= 0 && amount.trim() !== ""

  const fe = useFieldErrors(["unit_amount"], {keepOthers: false})
  const create = useMutation({
    mutationFn: () =>
      createPrice(
        {
          product_id: productId,
          type,
          unit_amount: cents,
          recurrence: {interval: "month", count: 1},
          // A metered price billed in advance would have to guess the usage it
          // charges for, and the domain refuses it. Decided here rather than
          // offered as a choice that is only valid half the time.
          billing_timing: type === "metered" ? "arrears" : "advance",
        },
        mode,
      ),
    onSuccess: () => {
      toast.success(t("console.product.createdDone"))
      setAmount("")
      onCreated()
    },
    onError: error => { if (!fe.set(error)) toast.error(messageFor(error)) },
  })

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("console.product.newTitle")}
      description={t("console.product.newBody")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.product.create")}
      submitDisabled={!valid}
      loading={create.isPending}
      onSubmit={() => { fe.reset(); create.mutate() }}
    >
      <div className="space-y-4">
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium text-foreground">{t("console.product.type")}</legend>
          <div className="flex gap-4">
            {(["fixed", "metered"] as const).map(option => (
              <label key={option} className="flex items-center gap-2 text-sm text-foreground">
                <input
                  type="radio"
                  name="price-type"
                  checked={type === option}
                  onChange={() => setType(option)}
                  className="size-4 accent-[var(--color-brand-600)]"
                />
                {option === "fixed" ? t("console.product.typeFixed") : t("console.product.typeMetered")}
              </label>
            ))}
          </div>
        </fieldset>

        <Field
          label={type === "metered" ? t("console.product.amountMetered") : t("console.product.amountFixed")}
          htmlFor="price-amount"
          error={fe.of("unit_amount")}
          hint={
            type === "metered"
              ? t("console.product.hintMetered")
              : t("console.product.hintFixed")
          }
        >
          <Input
            id="price-amount"
            inputMode="decimal"
            value={amount}
            {...fe.props("unit_amount", "price-amount")}
            onChange={event => { setAmount(maskMoney(event.target.value)); fe.clear("unit_amount") }}
            placeholder={moneyPlaceholder()}
          />
        </Field>
        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}
      </div>
    </Modal>
  )
}

function NotFound() {
  const {t} = useTranslation()
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
  )
}

function BackLink() {
  const {t} = useTranslation()
  return (
    <Link
      href="/console/catalog"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      {t("console.product.back")}
    </Link>
  )
}

function DetailSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-6 w-48"/>
      <Skeleton className="h-24 w-full"/>
    </div>
  )
}

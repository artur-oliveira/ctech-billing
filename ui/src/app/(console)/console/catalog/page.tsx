"use client"

import limits from "@/lib/limits.json"
import {Button, EmptyState, Field, Input, Modal, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {Package} from "lucide-react"
import Link from "next/link"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor} from "@/lib/api/client"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {consoleKeys, createProduct, listConsoleProducts} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"
import {money} from "@/lib/format"

/**
 * C8 — the catalogue.
 *
 * A product row shows how many prices it has and what the cheapest active one
 * costs, because "o que estamos vendendo e por quanto" is the question, and a
 * list of names answers half of it.
 */
export default function ConsoleCatalogPage() {
  const {t} = useTranslation()
  const mode = useMode()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)

  const query = useQuery({
    queryKey: consoleKeys.products(mode),
    queryFn: () => listConsoleProducts(mode),
  })

  const products = query.data?.data ?? []

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("console.catalog.title")}</h1>
        <Button size="sm" onClick={() => setCreating(true)}>{t("console.catalog.new")}</Button>
      </div>

      {query.isPending && <RowsSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && products.length === 0 && (
        <EmptyState
          icon={<Package/>}
          title={t("console.catalog.empty")}
          action={<Button onClick={() => setCreating(true)}>{t("console.catalog.new")}</Button>}
        />
      )}

      {products.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[36rem] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.catalog.product")}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.catalog.owner")}</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">{t("console.catalog.prices")}</th>
                <th scope="col" className="py-2 text-right font-medium">{t("console.catalog.from")}</th>
              </tr>
            </thead>
            <tbody>
              {products.map(product => {
                const active = (product.prices ?? []).filter(price => !price.archived)
                const cheapest = active.reduce<number | null>(
                  (min, price) => (min === null || price.unit_amount < min ? price.unit_amount : min),
                  null,
                )
                return (
                  <tr
                    key={product.id}
                    className="border-b border-border last:border-0 hover:bg-surface"
                  >
                    <td className="py-2 pr-4">
                      <Link
                        href={`/console/product?id=${product.id}`}
                        className="text-brand-600 underline-offset-4 hover:underline"
                      >
                        {product.name}
                      </Link>
                      {!product.active && (
                        <span className="ml-2 text-xs text-muted-foreground">{t("console.catalog.inactive")}</span>
                      )}
                    </td>
                    <td className="py-2 pr-4 text-muted-foreground">
                      {(product as {owner_key?: string}).owner_key || "—"}
                    </td>
                    <td data-numeric className="py-2 pr-4 text-right text-muted-foreground">
                      {active.length}
                    </td>
                    <td data-numeric className="py-2 text-right text-foreground">
                      {cheapest === null ? "—" : money(cheapest)}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      <NewProductDialog
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={() => {
          void queryClient.invalidateQueries({queryKey: consoleKeys.products(mode)})
          setCreating(false)
        }}
      />
    </div>
  )
}

function NewProductDialog({
  open,
  onClose,
  onCreated,
}: { open: boolean; onClose: () => void; onCreated: () => void }) {
  const {t} = useTranslation()
  const mode = useMode()
  const [name, setName] = useState("")
  const [ownerKey, setOwnerKey] = useState("")

  const fe = useFieldErrors(["name", "owner_key"], {keepOthers: false})
  const create = useMutation({
    mutationFn: () => createProduct({name: name.trim(), owner_key: ownerKey.trim()}, mode),
    onSuccess: () => {
      toast.success(t("console.catalog.created"))
      setName("")
      setOwnerKey("")
      onCreated()
    },
    onError: error => { if (!fe.set(error)) toast.error(messageFor(error)) },
  })

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("console.catalog.new")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.catalog.create")}
      submitDisabled={name.trim() === ""}
      loading={create.isPending}
      onSubmit={() => { fe.reset(); create.mutate() }}
    >
      <div className="space-y-4">
        <Field label={t("console.catalog.name")} htmlFor="product-name" error={fe.of("name")}>
          <Input
            id="product-name"
            maxLength={limits.text.productName}
            value={name}
            {...fe.props("name", "product-name")}
            onChange={event => { setName(event.target.value); fe.clear("name") }}
            placeholder="DF-e Avançado"
          />
        </Field>
        <Field
          label={t("console.catalog.ownerLabel")}
          htmlFor="product-owner"
          hint={t("console.catalog.ownerHint")}
          error={fe.of("owner_key")}
        >
          <Input
            id="product-owner"
            maxLength={limits.text.ownerKey}
            value={ownerKey}
            {...fe.props("owner_key", "product-owner")}
            onChange={event => { setOwnerKey(event.target.value); fe.clear("owner_key") }}
            placeholder="dfe"
          />
        </Field>
        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}
      </div>
    </Modal>
  )
}

function RowsSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2].map(i => <Skeleton key={i} className="h-8 w-full"/>)}
    </div>
  )
}

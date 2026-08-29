"use client"

import {Button, Field, Input, Modal, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ArrowLeft} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {toast} from "sonner"

import {DunningPolicyCard} from "@/components/console/DunningPolicyCard"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor, statusOf} from "@/lib/api/client"
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
      toast.success("Preço arquivado. Quem já assina continua no mesmo valor.")
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
          <Button size="sm" onClick={() => setPricing(true)}>Novo preço</Button>
        </div>
        {/* Said once, on the screen where it matters, rather than in a tooltip
            nobody opens. */}
        <p className="max-w-prose text-sm text-muted-foreground">
          Um preço não pode ser editado. Para mudar o valor, crie um novo preço — quem já assina
          continua no antigo — e arquive o anterior quando não quiser mais vendê-lo.
        </p>
      </header>

      <section aria-labelledby="precos" className="space-y-3">
        <h2 id="precos" className="text-sm font-medium text-muted-foreground">Preços</h2>
        {prices.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            Este produto ainda não tem preço, então não pode ser assinado.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[40rem] border-collapse text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th scope="col" className="py-2 pr-4 font-medium">Preço</th>
                  <th scope="col" className="py-2 pr-4 font-medium">Tipo</th>
                  <th scope="col" className="py-2 pr-4 font-medium">Recorrência</th>
                  <th scope="col" className="py-2 pr-4 text-right font-medium">Valor</th>
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
                        <span className="ml-2 text-xs text-muted-foreground">arquivado</span>
                      )}
                    </td>
                    <td className="py-2 pr-4 text-muted-foreground">
                      {price.type === "metered" ? "Por uso" : "Fixo"}
                    </td>
                    <td className="py-2 pr-4 text-muted-foreground">
                      {intervalLabel(price.recurrence.interval)}
                      {price.billing_timing === "arrears" ? " · no fim" : " · antecipado"}
                    </td>
                    <td data-numeric className="py-2 pr-4 text-right text-foreground">
                      {money(price.unit_amount, price.currency)}
                      {price.type === "metered" && (
                        <span className="text-muted-foreground"> /un.</span>
                      )}
                    </td>
                    <td className="py-2 text-right">
                      {!price.archived && (
                        <Button variant="outline" size="sm" onClick={() => setArchiving(price)}>
                          Arquivar
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
          title="Política de cobrança deste produto"
          description="Sobrepõe a política da organização para faturas que cobram este produto. Uma assinatura que cobra produtos com políticas diferentes volta para a da organização — não há como escolher entre duas."
          policy={product.dunning}
          inheritLabel="a política da organização"
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
        title="Arquivar este preço?"
        description="Ele deixa de aparecer para novas assinaturas. Quem já assina continua pagando o mesmo valor — arquivar não muda contrato de ninguém."
        cancelLabel="Manter"
        submitLabel="Arquivar preço"
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
  const mode = useMode()
  const [amount, setAmount] = useState("")
  const [type, setType] = useState<"fixed" | "metered">("fixed")

  const cents = Math.round(Number(amount.replace(",", ".")) * 100)
  const valid = Number.isFinite(cents) && cents >= 0 && amount.trim() !== ""

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
      toast.success("Preço criado.")
      setAmount("")
      onCreated()
    },
    onError: error => toast.error(messageFor(error)),
  })

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Novo preço"
      description="Mensal, em reais. Um preço criado não pode ser alterado depois."
      cancelLabel="Cancelar"
      submitLabel="Criar preço"
      submitDisabled={!valid}
      loading={create.isPending}
      onSubmit={() => create.mutate()}
    >
      <div className="space-y-4">
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium text-foreground">Tipo</legend>
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
                {option === "fixed" ? "Valor fixo por período" : "Por unidade consumida"}
              </label>
            ))}
          </div>
        </fieldset>

        <Field
          label={type === "metered" ? "Valor por unidade" : "Valor por período"}
          htmlFor="price-amount"
          hint={
            type === "metered"
              ? "Cobrado no fim do período, sobre o que foi reportado."
              : "Cobrado no início de cada período."
          }
        >
          <Input
            id="price-amount"
            inputMode="decimal"
            value={amount}
            onChange={event => setAmount(event.target.value)}
            placeholder="0,00"
          />
        </Field>
      </div>
    </Modal>
  )
}

function intervalLabel(interval: string): string {
  return {month: "mensal", year: "anual", week: "semanal", day: "diária"}[interval] ?? interval
}

function NotFound() {
  return (
    <div className="space-y-6">
      <BackLink/>
      <div className="space-y-2">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
          Produto não encontrado
        </h1>
        <p className="text-sm text-muted-foreground">
          Ele pode pertencer ao outro modo — confira se você está em Produção ou Teste.
        </p>
      </div>
    </div>
  )
}

function BackLink() {
  return (
    <Link
      href="/console/catalog"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      Catálogo
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

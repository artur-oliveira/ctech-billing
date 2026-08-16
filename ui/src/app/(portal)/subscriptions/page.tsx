"use client"

import {Button, EmptyState, Modal, PageHeader, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {Repeat} from "lucide-react"
import {useState} from "react"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Money} from "@/components/portal/Money"
import {StatusBadge} from "@/components/portal/StatusBadge"
import {messageFor} from "@/lib/api/client"
import {cancelSubscription, listSubscriptions, portalKeys} from "@/lib/api/portal"
import type {Subscription} from "@/lib/api/types"
import {longDate} from "@/lib/format"

export default function SubscriptionsPage() {
  const queryClient = useQueryClient()
  const [cancelling, setCancelling] = useState<Subscription | null>(null)

  const query = useQuery({queryKey: portalKeys.subscriptions, queryFn: listSubscriptions})

  const cancel = useMutation({
    mutationFn: (id: string) => cancelSubscription(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({queryKey: portalKeys.subscriptions})
      setCancelling(null)
      toast.success("Assinatura encerrada no fim do período atual.")
    },
    onError: error => toast.error(messageFor(error)),
  })

  const subscriptions = query.data?.data ?? []

  return (
    <div className="space-y-8">
      <PageHeader title="Assinaturas"/>

      {query.isPending && <SubscriptionsSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && subscriptions.length === 0 && (
        <EmptyState
          icon={<Repeat/>}
          title="Você não tem assinaturas"
          description="Planos contratados com a CTech aparecem aqui, com o valor e a data da próxima cobrança."
        />
      )}

      {subscriptions.length > 0 && (
        /* Rows on a rule, not a stack of cards. Every subscription here is the
           same kind of thing, and boxing each one separately spends a border
           per item to say so — which reads as N competing panels rather than
           one list. */
        <ul className="divide-y divide-border border-y border-border">
          {subscriptions.map(s => (
            <li key={s.id} className="py-5">
              <div className="flex flex-wrap items-start justify-between gap-4">
                <div className="space-y-2">
                  <p className="font-medium text-foreground">{s.description}</p>
                  <StatusBadge state={s.state} tone={s.tone}/>
                </div>
                {s.metered ? (
                  <span className="text-sm text-muted-foreground">Valor conforme o uso</span>
                ) : (
                  s.amount != null && <Money cents={s.amount} currency={s.currency}/>
                )}
              </div>

              <p className="mt-4 text-sm text-muted-foreground">
                {s.renews_on
                  ? `Próxima cobrança em ${longDate(s.renews_on)}`
                  : `Ativa até ${longDate(s.current_period.end)}`}
              </p>

              {/* Cancelling is offered plainly. A subscription whose exit a
                  person cannot find is a subscription they cancel by disputing
                  the charge with their bank instead. */}
              {s.cancelable && s.renews_on && (
                <Button
                  variant="outline"
                  size="sm"
                  className="mt-4"
                  onClick={() => setCancelling(s)}
                >
                  Cancelar assinatura
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}

      <Modal
        open={cancelling !== null}
        onClose={() => setCancelling(null)}
        title="Cancelar esta assinatura?"
        // Period-end only, and the modal says so rather than letting somebody
        // discover it. The portal never offers immediate cancellation: mid-period
        // means money back, money back is a credit note, and that is a decision
        // a person makes in the console.
        // Built around the plan name rather than starting with it: "Emissões
        // adicionais continua funcionando" forces a verb to agree with a
        // product name whose number nobody controls.
        description={
          cancelling
            ? `Você mantém o acesso a ${cancelling.description} até ${longDate(cancelling.current_period.end)}. Não haverá nova cobrança depois disso.`
            : undefined
        }
        cancelLabel="Manter assinatura"
        submitLabel="Cancelar no fim do período"
        danger
        loading={cancel.isPending}
        onSubmit={() => cancelling && cancel.mutate(cancelling.id)}
      />
    </div>
  )
}

function SubscriptionsSkeleton() {
  return (
    <ul className="divide-y divide-border border-y border-border" aria-busy>
      {[0, 1].map(i => (
        <li key={i} className="space-y-4 py-5">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <Skeleton className="h-5 w-40"/>
              <Skeleton className="h-5 w-24 rounded-full"/>
            </div>
            <Skeleton className="h-5 w-20"/>
          </div>
          <Skeleton className="h-4 w-56"/>
        </li>
      ))}
    </ul>
  )
}

import type {AuditEntry} from "@/lib/api/consoleTypes"

/**
 * The audit trail, as the screen an operator actually reads during an incident.
 *
 * Every entry names who, what, why and when, and carries the request id —
 * because a support conversation that cannot name the request is one that goes
 * in circles. The actor is rendered raw (`user:01J…`, `service:ctech-wallet`,
 * `scheduler`) rather than prettified: the prefix is the distinction that
 * matters, and inventing display names for it would make two different kinds of
 * actor look like one.
 */
const ACTION: Record<string, string> = {
  "invoice.created": "Fatura criada",
  "invoice.finalized": "Fatura emitida",
  "invoice.paid": "Fatura paga",
  "invoice.voided": "Fatura anulada",
  "invoice.payment_failed": "Tentativa de cobrança falhou",
  "invoice.uncollectible": "Marcada como incobrável",
  "credit_note.created": "Nota de crédito emitida",
  "subscription.created": "Assinatura criada",
  "subscription.canceled": "Assinatura cancelada",
  "customer.created": "Cliente criado",
  "price.created": "Preço criado",
  "price.archived": "Preço arquivado",
  "product.created": "Produto criado",
}

const stamp = new Intl.DateTimeFormat("pt-BR", {
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
})

export function Timeline({entries}: { entries: AuditEntry[] }) {
  if (entries.length === 0) return null

  return (
    <section aria-labelledby="historico" className="space-y-3">
      <h2 id="historico" className="text-sm font-medium text-muted-foreground">Histórico</h2>
      <ul className="divide-y divide-border border-y border-border">
        {entries.map(entry => (
          <li key={entry.id} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-2">
            <div className="min-w-0 space-y-0.5">
              <p className="text-sm text-foreground">
                {ACTION[entry.action] ?? entry.action}
                {entry.before && entry.after && (
                  <span className="ml-2 text-xs text-muted-foreground">
                    {entry.before} → {entry.after}
                  </span>
                )}
              </p>
              {/* The cause is dropped when it merely repeats the actor —
                  "scheduler · scheduler" is two fields saying one thing, and a
                  timeline read during an incident should carry only what
                  differs. */}
              <p className="text-xs text-muted-foreground">
                {entry.actor}
                {entry.cause && entry.cause !== entry.actor && ` · ${entry.cause}`}
                {entry.request_id && ` · ${entry.request_id}`}
              </p>
            </div>
            <time
              data-numeric
              dateTime={entry.created_at}
              className="shrink-0 text-xs text-muted-foreground"
            >
              {stamp.format(new Date(entry.created_at))}
            </time>
          </li>
        ))}
      </ul>
    </section>
  )
}

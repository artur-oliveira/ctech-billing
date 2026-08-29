"use client"

import {Button} from "@aoctech/ui"
import {useMutation} from "@tanstack/react-query"
import {Download} from "lucide-react"
import {toast} from "sonner"

import {messageFor} from "@/lib/api/client"
import type {DocumentLink} from "@/lib/api/types"

/**
 * The invoice's PDF, on both shells.
 *
 * The server answers with a short-lived signed URL rather than the bytes, so
 * this navigates to it instead of building a blob: the link already carries a
 * `Content-Disposition`, which is what makes the browser save "fatura-1042.pdf"
 * rather than open a tab named after a ULID.
 *
 * `window.open` and not an `<a download>`: the file lives on another origin
 * (S3), where the `download` attribute is ignored, and the request needs an
 * Authorization header the anchor cannot send. The click asks the API, the API
 * signs, and the browser follows.
 *
 * The first click on an invoice nobody has downloaded before renders the
 * document server-side, so it is slower — hence a pending label rather than an
 * optimistic one.
 */
export function DownloadPDF({
  fetchLink,
  label = "Baixar PDF",
  size = "sm",
  variant = "outline",
}: {
  fetchLink: () => Promise<DocumentLink>
  label?: string
  size?: "sm" | "default"
  variant?: "outline" | "ghost"
}) {
  const download = useMutation({
    mutationFn: fetchLink,
    onSuccess: link => {
      // Opened in the same tab's navigation rather than a new one: a popup
      // blocker eats a `window.open` that is not in the click's own task, and
      // this one is not — it follows an await.
      window.location.href = link.url
    },
    onError: error => toast.error(messageFor(error)),
  })

  return (
    <Button
      variant={variant}
      size={size}
      onClick={() => download.mutate()}
      disabled={download.isPending}
    >
      <Download aria-hidden className="size-3.5"/>
      {download.isPending ? "Gerando…" : label}
    </Button>
  )
}

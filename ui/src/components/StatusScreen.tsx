import Image from "next/image"
import Link from "next/link"
import type {ReactNode} from "react"

/**
 * The whole-page states: not found, broken, under maintenance.
 *
 * These render outside the portal shell on purpose. All three mean the nav is
 * either useless (a URL that does not exist) or actively misleading (three
 * links to screens that are also down), and a header that pretends the
 * product is working is worse than no header. What stays is the wordmark, so
 * the reader still knows whose page failed.
 */
export function StatusScreen({
  title,
  description,
  action,
}: {
  title: string
  description: string
  action?: ReactNode
}) {
  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col items-center justify-center gap-4 px-6 py-16 text-center">
      <Link href="/" className="flex flex-col items-center gap-3">
        <Image
          src="/android-chrome-192x192.png"
          alt=""
          width={40}
          height={40}
          priority
          className="size-10 rounded-xl"
        />
        <span className="text-sm font-semibold tracking-[-0.01em] text-brand-600">
          CTech
          <span className="ml-1.5 font-normal text-muted-foreground">Billing</span>
        </span>
      </Link>
      <h1 className="mt-2 text-balance text-xl font-medium text-foreground">{title}</h1>
      <p className="max-w-[46ch] text-pretty text-sm text-muted-foreground">{description}</p>
      {action && <div className="mt-2">{action}</div>}
    </main>
  )
}

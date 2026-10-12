"use client"

import {Select} from "@aoctech/ui"
import Link from "next/link"
import {usePathname, useRouter} from "next/navigation"
import {useTranslation} from "react-i18next"

import {selectCopy} from "@/lib/selectCopy"

export type SectionNavItem = {href: string; label: string; exact?: boolean}

export function SectionNav({items, label, selectLabel}: {items: SectionNavItem[]; label: string; selectLabel: string}) {
  const pathname = usePathname()
  const router = useRouter()
  const {t} = useTranslation()
  const current = items.find(item => item.exact ? pathname === item.href : pathname.startsWith(item.href))?.href ?? items[0]?.href ?? ""

  if (items.length === 0) return null

  return <>
    <div className="hidden md:block lg:hidden">
      <Select {...selectCopy()} aria-label={selectLabel} value={current} onValueChange={href => router.push(href)} options={items.map(item => ({value: item.href, label: t(item.label)}))}/>
    </div>
    <nav aria-label={label} className="hidden lg:block">
      <ul className="space-y-0.5 border-l border-border">
        {items.map(item => {
          const active = item.href === current
          return <li key={item.href}>
            <Link href={item.href} aria-current={active ? "page" : undefined} className={`-ml-px flex h-8 items-center border-l-2 pl-3 text-sm transition-colors ${active ? "border-brand-600 font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"}`}>
              {t(item.label)}
            </Link>
          </li>
        })}
      </ul>
    </nav>
  </>
}

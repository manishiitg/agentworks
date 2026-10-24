import { followAppLink } from '../../utils/appNavigation'
import type { ComponentType } from 'react'

export type ProductMenuItem = {
  id: string
  label: string
  icon?: ComponentType<{ className?: string }>
  active?: boolean
  disabled?: boolean
  detail?: string
  attention?: boolean
  href?: string
  external?: boolean
  onSelect?: () => void
}

/** Destinations are supplied by the product after its existing access checks. */
export function ProductMenuItems({ items, onSelect }: { items: readonly ProductMenuItem[]; onSelect: () => void }) {
  return <div role="group" aria-label="Product pages" className="p-1">{items.map(item => {
    const className = `flex min-h-8 w-full items-center gap-2 rounded-md px-2 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 ${item.attention ? 'text-amber-500' : item.active ? 'bg-muted/60 text-foreground' : 'text-muted-foreground'}`
    const content = <>{item.icon && <item.icon className="h-4 w-4 shrink-0" />}<span>{item.label}</span>{item.detail && <span className="ml-auto text-[11px]">{item.detail}</span>}{item.external && <span className="sr-only"> (opens in a new tab)</span>}</>
    return item.href ? <a key={item.id} role="menuitem" href={item.href} target={item.external ? '_blank' : undefined} rel={item.external ? 'noopener noreferrer' : undefined} className={className} onClick={event => { onSelect(); if (!item.external) followAppLink(event) }}>{content}</a>
      : <button key={item.id} type="button" role="menuitem" disabled={item.disabled} aria-current={item.active ? 'page' : undefined} onClick={() => { onSelect(); item.onSelect?.() }} className={className}>{content}</button>
  })}</div>
}

import type { ComponentType } from 'react'

export type ProductSwitchOption = { id: string; label: string; description: string; icon: ComponentType<{ className?: string; title?: string }> }

/** One product grid for workspace pickers, service headers and page headers. */
export function ProductSwitchMenu({ products, current, onSelect, popup = false, onEscape, className = '' }: {
  products: readonly ProductSwitchOption[]
  current: string
  onSelect: (id: string) => void
  popup?: boolean
  onEscape?: () => void
  className?: string
}) {
  return <section aria-label="Products" role={popup ? 'menu' : undefined} data-product-switch-menu
    className={`${popup ? 'rounded-lg border border-border bg-popover text-popover-foreground shadow-xl' : 'shrink-0 border-t border-border'} p-1.5 ${className}`}
    onKeyDown={event => {
      if (event.key === 'Escape') { onEscape?.(); return }
      if (!['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
      event.preventDefault()
      const items = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-product-option]')]
      const index = items.indexOf(document.activeElement as HTMLButtonElement)
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowUp' || event.key === 'ArrowLeft' ? -1 : 1) + items.length) % items.length
      items[next]?.focus()
    }}>
    <h3 className="px-1 pb-1 text-[11px] font-medium text-muted-foreground">Switch product</h3>
    <div className="grid grid-cols-2 gap-0.5 sm:grid-cols-3">
      {products.map(product => <button key={product.id} type="button" role={popup ? 'menuitem' : undefined} data-product-option
        onClick={() => onSelect(product.id)} aria-label={product.label} aria-current={product.id === current ? 'page' : undefined} title={product.description}
        className={`flex min-h-7 items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${product.id === current ? 'bg-muted/60' : ''}`}>
        <product.icon className="h-4 w-4 shrink-0" title="" /><span>{product.label}</span>
      </button>)}
    </div>
  </section>
}

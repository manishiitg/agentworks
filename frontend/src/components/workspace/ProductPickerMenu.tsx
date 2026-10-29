import { createContext, useContext, type ReactNode } from 'react'

const ProductPickerContext = createContext(false)
export const useInProductPicker = () => useContext(ProductPickerContext)

/** Project pickers and product page pickers share one layout and keyboard path. */
export function ProductPickerMenu({ title, children, products, footer, onEscape }: {
  title: string
  children: ReactNode
  products?: ReactNode
  footer?: ReactNode
  onEscape?: () => void
}) {
  return <ProductPickerContext.Provider value={true}><div data-product-picker-menu className="flex min-h-0 flex-1 flex-col" onKeyDown={event => {
    if (event.defaultPrevented) return
    if (event.key === 'Escape') { onEscape?.(); return }
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const items = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), a[href]')]
    const index = items.indexOf(document.activeElement as HTMLElement)
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length
    items[next]?.focus()
  }}>
    <header className="shrink-0 border-b border-border bg-popover px-3 py-2 text-xs font-semibold">{title}</header>
    <div data-picker-workspaces className="min-h-0 max-h-[min(260px,35dvh)] overflow-y-auto [&>div]:max-h-none [&>div]:space-y-0 [&>div]:p-1 [&_button]:py-1.5 [&_button]:text-xs">{children}</div>
    {products}
    {footer}
  </div></ProductPickerContext.Provider>
}

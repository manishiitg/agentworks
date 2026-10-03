import type { ReactNode } from 'react'
import { Plus } from 'lucide-react'

interface ProductIntroProps {
  tour: string
  product: string
  icon: ReactNode
  title: string
  description: string
  features: { title: string; description: string }[]
  createLabel: string
  createTour: string
  onCreate: () => void
  canCreate?: boolean
  createUnavailableMessage?: string
  footer?: ReactNode
}

// Shared introduction used before a project is selected in Crew, Code and Relays.
export function ProductIntro({ tour, product, icon, title, description, features, createLabel, createTour, onCreate, canCreate = true, createUnavailableMessage, footer }: ProductIntroProps) {
  return (
    <div data-tour={tour} className="flex max-w-xl flex-col items-center px-6 text-center">
      <div aria-hidden="true" className="flex h-20 w-20 shrink-0 items-center justify-center rounded-full bg-gray-200 dark:bg-gray-700 [@media(max-height:600px)]:hidden">
        {icon}
      </div>
      <p className="mt-5 text-xs font-semibold uppercase tracking-[0.18em] text-primary">{product}</p>
      <h2 className="mt-2 text-2xl font-semibold text-gray-900 dark:text-gray-100">{title}</h2>
      <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-muted-foreground">{description}</p>
      <div className="mt-5 grid grid-cols-1 gap-2 text-left text-xs text-muted-foreground sm:grid-cols-3">
        {features.map(feature => (
          <div key={feature.title} className="rounded-lg border border-border bg-background/70 px-3 py-2.5">
            <span className="block font-medium text-foreground">{feature.title}</span>
            {feature.description}
          </div>
        ))}
      </div>
      {footer && <p className="mx-auto mt-4 max-w-lg text-xs leading-5 text-muted-foreground">{footer}</p>}
      <button
        data-tour={createTour}
        type="button"
        onClick={onCreate}
        disabled={!canCreate}
        className="mt-5 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
      >
        <Plus className="h-3.5 w-3.5" /> {createLabel}
      </button>
      {!canCreate && createUnavailableMessage && <p className="mt-2 text-xs text-muted-foreground">{createUnavailableMessage}</p>}
    </div>
  )
}

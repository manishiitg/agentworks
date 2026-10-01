import { createContext, useContext, type ReactNode } from 'react'

const UnboxedSettingsContext = createContext(false)

/** Settings sections can use the surrounding pane instead of drawing another box. */
export function SettingsCardLayout({ unboxed = false, children }: { unboxed?: boolean; children: ReactNode }) {
  return <UnboxedSettingsContext.Provider value={unboxed}>{children}</UnboxedSettingsContext.Provider>
}

export interface SettingsCardProps {
  /** Leading icon, e.g. `<KeyRound className="h-4 w-4 text-primary" />`. */
  icon?: ReactNode
  title: ReactNode
  /** Trailing count pill content, e.g. `"3 attached"`. Omit when not countable. */
  count?: ReactNode
  /** Right-side header action (e.g. an Add button or badge). */
  actions?: ReactNode
  /** Plain-words explainer under the header. */
  description?: ReactNode
  children?: ReactNode
  className?: string
  ariaLabel?: string
  /** Override the surrounding settings layout for this section. */
  unboxed?: boolean
}

/**
 * The standard settings card: icon + semibold title + count pill, an
 * optional right-side action, a muted explainer, then content. Every
 * settings tab builds its blocks from this so cards stay identical across
 * panes. Base text is xs with an sm title; form controls keep their own
 * sizes for hierarchy.
 */
export function SettingsCard({ icon, title, count, actions, description, children, className, ariaLabel, unboxed }: SettingsCardProps) {
  const inheritedUnboxed = useContext(UnboxedSettingsContext)
  const isUnboxed = unboxed ?? inheritedUnboxed
  return (
    <section aria-label={ariaLabel} className={`space-y-3 ${isUnboxed ? '' : 'rounded-lg border border-border p-4'} text-xs ${className ?? ''}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          {icon}
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          {count !== undefined && <SettingsCount>{count}</SettingsCount>}
        </div>
        {actions}
      </div>
      {description && <div className="text-muted-foreground">{description}</div>}
      {children}
    </section>
  )
}

/** Muted count pill for a card header, e.g. "3 attached". */
export function SettingsCount({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full bg-muted px-2 py-0.5 text-muted-foreground">
      {children}
    </span>
  )
}

/** Dashed empty-state box for card bodies. */
export function SettingsEmpty({ children }: { children: ReactNode }) {
  return (
    <p className="rounded border border-dashed p-3 text-muted-foreground">
      {children}
    </p>
  )
}

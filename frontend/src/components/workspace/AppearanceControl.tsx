import { ChevronRight, Palette } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { AppearanceAgentButton } from './AppearanceAgentButton'
import { AppearanceThemePicker } from './AppearanceThemePicker'

/** Same small picker in the account menu and Settings; customization opens an own chat. */
export default function AppearanceControl({ compact = false }: { compact?: boolean } = {}) {
  const current = useAppearanceStore(state => state.current)
  const error = useAppearanceStore(state => state.error)
  return <details className="group/appearance mt-1 border-t border-border px-3 py-2">
    <summary role="menuitem" className="flex min-h-8 cursor-pointer list-none items-center gap-2 rounded-md text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      {!compact && <Palette aria-hidden="true" className="h-3.5 w-3.5" />}
      <span>Appearance</span><span className="ml-auto min-w-0 truncate text-muted-foreground">{current.name === 'Default' ? `Default ${current.mode === 'light' ? 'Light' : 'Dark'}` : current.name}</span><ChevronRight aria-hidden="true" className="h-3.5 w-3.5 shrink-0 group-open/appearance:rotate-90" />
    </summary>
    <div className="space-y-2 pt-2">
      <AppearanceThemePicker menu />
      <AppearanceAgentButton menu className="w-full border-t border-border" />
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
  </details>
}

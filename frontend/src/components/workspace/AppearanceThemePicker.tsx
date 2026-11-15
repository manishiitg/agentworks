import { Check, Trash2 } from 'lucide-react'
import { defaultAppearance } from '../../stores/appearanceTheme'
import { useAppearanceStore } from '../../stores/useAppearanceStore'

/** The account menu and Settings show one personal library, not separate preset types. */
export function AppearanceThemePicker({ menu = false }: { menu?: boolean }) {
  const current = useAppearanceStore(state => state.current)
  const saved = useAppearanceStore(state => state.saved)
  const saving = useAppearanceStore(state => state.saving)
  const change = useAppearanceStore(state => state.change)
  const choice = 'flex min-h-9 min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-left text-xs text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const semantics = (selected: boolean) => menu ? { role: 'menuitemradio' as const, 'aria-checked': selected } : { 'aria-pressed': selected }
  return <div role="group" aria-label="Themes" className="space-y-1">
    <div className="flex gap-1 rounded-lg bg-muted p-1">
      {(['light', 'dark'] as const).map(mode => {
        const selected = current.name === 'Default' && current.mode === mode
        return <button key={mode} type="button" {...semantics(selected)} disabled={saving} onClick={() => void change('apply', { ...defaultAppearance(), mode })} className={`${choice} ${selected ? 'bg-card font-medium shadow-sm' : ''}`}><Check aria-hidden="true" className={`h-3 w-3 shrink-0 ${selected ? '' : 'invisible'}`} /><span>Default {mode === 'light' ? 'Light' : 'Dark'}</span></button>
      })}
    </div>
    <div className="max-h-52 overflow-y-auto">{saved.map(theme => <div key={theme.name} className="flex items-center gap-1">
      <button type="button" {...semantics(current.name === theme.name)} disabled={saving} onClick={() => void change('apply', theme)} className={choice}><Check aria-hidden="true" className={`h-3 w-3 shrink-0 ${current.name === theme.name ? '' : 'invisible'}`} /><span className="min-w-0 truncate">{theme.name}</span></button>
      <button type="button" role={menu ? 'menuitem' : undefined} aria-label={`Delete saved theme ${theme.name}`} title="Delete the saved theme; the current look stays until you choose another" disabled={saving} onClick={() => void change('delete', undefined, theme.name)} className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Trash2 aria-hidden="true" className="h-3.5 w-3.5" /></button>
    </div>)}</div>
  </div>
}

import { Check, ChevronRight, Palette } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'

/** One account entry. Mode and saved looks stay together, with no nested theme menu. */
export default function AppearanceControl({ compact = false }: { compact?: boolean } = {}) {
  const { profile, setProfile, current, saved, saving, error, change } = useAppearanceStore()
  const choice = 'flex min-h-8 w-full items-center gap-2 rounded-md px-2 text-left text-xs text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <details className="group/appearance mt-1 border-t border-border px-3 py-2">
    <summary role="menuitem" className="flex min-h-8 cursor-pointer list-none items-center gap-2 rounded-md text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      {!compact && <Palette aria-hidden="true" className="h-3.5 w-3.5" />}
      <span>Appearance</span><span className="ml-auto min-w-0 truncate text-muted-foreground">{current.name}</span><ChevronRight aria-hidden="true" className="h-3.5 w-3.5 shrink-0 group-open/appearance:rotate-90" />
    </summary>
    <div role="group" aria-label="Appearance" className="space-y-2 pt-2">
      <div className="flex rounded-lg bg-muted p-1" aria-label="Colour mode">
        {(['light', 'dark'] as const).map(value => <button key={value} type="button" role="menuitemradio" aria-checked={profile === value} disabled={saving} onClick={() => setProfile(value)} className={`flex min-h-8 flex-1 items-center justify-center gap-1 rounded-md text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${profile === value ? 'bg-card font-medium text-foreground shadow-sm' : 'text-muted-foreground'}`}><Check aria-hidden="true" className={`h-3 w-3 ${profile === value ? '' : 'invisible'}`} />{value === 'dark' ? 'Dark' : 'Light'}</button>)}
      </div>
      <div role="group" aria-label="Themes" className="max-h-44 overflow-y-auto">
        <button type="button" role="menuitemradio" aria-checked={current.name === 'Default'} disabled={saving} title="Restore the default look; keep your saved themes" onClick={() => void change('reset')} className={choice}><Check aria-hidden="true" className={`h-3 w-3 ${current.name === 'Default' ? '' : 'invisible'}`} />Default</button>
        {saved.map(theme => <button key={theme.name} type="button" role="menuitemradio" aria-checked={current.name === theme.name} disabled={saving} onClick={() => void change('apply', theme)} className={choice}><Check className={`h-3 w-3 ${current.name === theme.name ? '' : 'invisible'}`} aria-hidden="true"/><span className="min-w-0 truncate">{theme.name}</span></button>)}
      </div>
      <button type="button" role="menuitem" data-product-navigation-action onClick={() => openWorkspaceSettings('connections', 'appearance')} className={`${choice} border-t border-border pt-2`}><Palette className="h-3.5 w-3.5" aria-hidden="true" /><span className="flex-1">Customize with an agent</span><ChevronRight className="h-3.5 w-3.5" aria-hidden="true" /></button>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
  </details>
}

import { Check, ChevronRight, Palette } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'

/** One inline menu; Default clears customization, Light/Dark change its mode. */
export default function AppearanceControl({ compact = false }: { compact?: boolean } = {}) {
  const { profile, setProfile, current, saved, saving, error, change } = useAppearanceStore()
  return <div role="group" aria-label="Appearance" className="mt-1 space-y-2 border-t border-border px-3 py-2">
    <div className="flex items-center gap-2">
      <span className="flex items-center gap-2 text-xs text-muted-foreground">{!compact && <Palette aria-hidden="true" className="h-3.5 w-3.5" />}Theme</span>
      <div className="ml-auto flex gap-1">
        <button type="button" disabled={saving} title="Restore the default look; keep your saved themes" onClick={() => void change('reset')} className="min-h-8 rounded-md px-2 text-xs text-muted-foreground hover:bg-muted">Default</button>
        {(['light', 'dark'] as const).map(value => <button key={value} type="button" role="menuitemradio" aria-checked={profile === value} disabled={saving} onClick={() => setProfile(value)} className={`flex min-h-8 items-center gap-1 rounded-md px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${profile === value ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:bg-muted'}`}><Check aria-hidden="true" className={`h-3 w-3 ${profile === value ? '' : 'invisible'}`} />{value === 'dark' ? 'Dark' : 'Light'}</button>)}
      </div>
    </div>
    {saved.length > 0 && <label className="flex items-center gap-2 text-xs text-muted-foreground">Saved
      <select aria-label="Saved themes" value={saved.some(theme => theme.name === current.name) ? current.name : ''} disabled={saving} className="min-h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-xs text-foreground" onChange={event => { const theme = saved.find(item => item.name === event.target.value); if (theme) void change('apply', theme) }}>
        <option value="" disabled>Choose a theme…</option>{saved.map(theme => <option key={theme.name} value={theme.name}>{theme.name}</option>)}
      </select>
    </label>}
    <button type="button" role="menuitem" data-product-navigation-action onClick={() => openWorkspaceSettings('connections', 'appearance')} className="flex min-h-8 w-full items-center gap-2 rounded-md text-left text-xs text-foreground hover:bg-muted"><Palette className="h-3.5 w-3.5" aria-hidden="true" /><span className="flex-1">Appearance & more themes…</span><ChevronRight className="h-3.5 w-3.5" aria-hidden="true" /></button>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}

import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { AppearanceAgentButton } from './AppearanceAgentButton'
import { AppearanceThemePicker } from './AppearanceThemePicker'

export default function AppearanceSettings() {
  const current = useAppearanceStore(state => state.current)
  const change = useAppearanceStore(state => state.change)
  const saving = useAppearanceStore(state => state.saving)
  const error = useAppearanceStore(state => state.error)
  const currentLabel = current.name === 'Default' ? (current.mode === 'light' ? 'Light' : 'Dark') : current.name
  return <div className="max-w-md space-y-4">
    <p className="text-sm text-muted-foreground">Choose a look, or ask an agent to make it yours. This changes only your appearance.</p>
    <AppearanceThemePicker />
    {current.background_script && <button type="button" disabled={saving} className="text-sm underline" onClick={() => void change('apply', { ...current, background_script: null })}>Disable script background</button>}
    <AppearanceAgentButton className="w-full" />
    <p className="text-xs text-muted-foreground">Current look: <span className="text-foreground">{currentLabel}</span></p>
    {error && <p role="alert" className="rounded-md bg-destructive/10 px-2.5 py-2 text-sm text-destructive">{error}</p>}
  </div>
}

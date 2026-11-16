import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { AppearanceAgentButton } from './AppearanceAgentButton'
import { AppearanceThemePicker } from './AppearanceThemePicker'

export default function AppearanceSettings() {
  const current = useAppearanceStore(state => state.current)
  const change = useAppearanceStore(state => state.change)
  const saving = useAppearanceStore(state => state.saving)
  const error = useAppearanceStore(state => state.error)
  return <div className="max-w-lg space-y-5">
    <p className="text-sm text-muted-foreground">Choose a look, or ask an agent to make it yours. This changes only your appearance.</p>
    <AppearanceThemePicker />
    {current.background_script && <button type="button" disabled={saving} className="text-sm underline" onClick={() => void change('apply', {...current, background_script:null})}>Disable script background</button>}
    <AppearanceAgentButton className="w-full border border-border" />
    <p className="text-xs text-muted-foreground">Current look: <span className="text-foreground">{current.name === 'Default' ? `Default ${current.mode === 'light' ? 'Light' : 'Dark'}` : current.name}</span></p>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
  </div>
}

import { Check, Palette } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'

/** Inline choices avoid opening a second, portaled menu over Account & settings. */
export default function AppearanceControl() {
  const profile = useAppearanceStore(state => state.profile)
  const setProfile = useAppearanceStore(state => state.setProfile)
  return <div role="group" aria-label="Appearance" className="mt-1 border-t border-border pt-2">
    <div className="flex items-center gap-2 px-3 pb-1 text-[11px] font-medium text-muted-foreground">
      <Palette aria-hidden="true" className="h-3.5 w-3.5" />Appearance
    </div>
    <div className="flex gap-1 px-1">
      {(['dark', 'light'] as const).map(value => <button key={value} type="button" role="menuitemradio"
        aria-checked={profile === value} tabIndex={-1} onClick={() => setProfile(value)}
        className={`flex min-h-8 flex-1 items-center justify-center gap-1.5 rounded-md px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${profile === value ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        <Check aria-hidden="true" className={`h-3 w-3 ${profile === value ? '' : 'invisible'}`} />
        {value === 'dark' ? 'Default' : 'Light'}
      </button>)}
    </div>
  </div>
}

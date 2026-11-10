import { Palette } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../ui/select'

export default function AppearanceControl() {
  const profile = useAppearanceStore(state => state.profile)
  const setProfile = useAppearanceStore(state => state.setProfile)
  return <Select value={profile} onValueChange={value => setProfile(value === 'light' ? 'light' : 'dark')}>
    <SelectTrigger aria-label="Appearance" className="h-7 w-auto gap-1.5 border-transparent px-2 text-xs shadow-none">
      <Palette aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" /><SelectValue />
    </SelectTrigger>
    <SelectContent align="end">
      <SelectItem value="dark">Dark</SelectItem>
      <SelectItem value="light">Light</SelectItem>
      <p className="max-w-56 px-2 py-1.5 text-xs text-muted-foreground">System font for replies and menus. SF Mono for code and terminals.</p>
    </SelectContent>
  </Select>
}

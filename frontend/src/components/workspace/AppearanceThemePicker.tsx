import { Check, ChevronDown, Moon, Sun, Trash2 } from 'lucide-react'
import { defaultAppearance, INTERFACE_FONTS, type AppearanceTheme } from '../../stores/appearanceTheme'
import { useAppearanceStore } from '../../stores/useAppearanceStore'

function themeSwatch(theme: AppearanceTheme): { bg: string; accent: string } {
  const palette = theme.colors[theme.mode] ?? theme.colors.dark ?? theme.colors.light
  const bg = palette?.background || (theme.mode === 'light' ? '#f8f8fa' : '#18181b')
  const accent = palette?.accent || palette?.foreground || (theme.mode === 'light' ? '#18181b' : '#e4e4e7')
  return { bg, accent }
}

/** The account menu and Settings show one personal library, not separate preset types. */
export function AppearanceThemePicker({ menu = false }: { menu?: boolean }) {
  const current = useAppearanceStore(state => state.current)
  const saved = useAppearanceStore(state => state.saved)
  const saving = useAppearanceStore(state => state.saving)
  const change = useAppearanceStore(state => state.change)
  const fonts = useAppearanceStore(state => state.guide?.fonts)
  const typography = (patch: { interface_font?: string; interface_size?: number }) => void change('apply', patch, undefined, false)
  const semantics = (selected: boolean) => menu ? { role: 'menuitemradio' as const, 'aria-checked': selected } : { 'aria-pressed': selected }

  return <div role="group" aria-label="Themes" className="space-y-2.5">
    <div className="grid grid-cols-2 gap-1 rounded-lg bg-muted p-1">
      {(['light', 'dark'] as const).map(mode => {
        const selected = current.name === 'Default' && current.mode === mode
        const Icon = mode === 'light' ? Sun : Moon
        return <button
          key={mode}
          type="button"
          {...semantics(selected)}
          disabled={saving}
          onClick={() => void change('apply', { ...defaultAppearance(), mode })}
          className={`flex h-7 max-md:h-9 min-w-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-md px-2 text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'bg-card font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
        >
          <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
          <span>{mode === 'light' ? 'Light' : 'Dark'}</span>
        </button>
      })}
    </div>

    {saved.length > 0 && <div>
      <p className="px-1 pb-1 text-[11px] font-medium text-muted-foreground">Saved themes</p>
      <div className="max-h-44 space-y-0.5 overflow-y-auto">
        {saved.map(theme => {
          const selected = current.name === theme.name
          const { bg, accent } = themeSwatch(theme)
          return <div key={theme.name} className={`group flex items-center gap-1 rounded-md px-1 transition-colors ${selected ? 'bg-muted' : 'hover:bg-muted/70'}`}>
            <button
              type="button"
              {...semantics(selected)}
              disabled={saving}
              onClick={() => void change('apply', theme)}
              className={`flex h-8 max-md:h-9 min-w-0 flex-1 items-center gap-2 rounded-md px-1 text-left text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'font-medium' : ''}`}
            >
              <span aria-hidden="true" className="relative h-3.5 w-3.5 shrink-0 overflow-hidden rounded-full border border-border" style={{ backgroundColor: bg }}>
                <span className="absolute inset-y-0 right-0 w-1/2" style={{ backgroundColor: accent }} />
              </span>
              <span className="min-w-0 flex-1 truncate">{theme.name}</span>
              {selected && <Check aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-foreground" />}
            </button>
            <button
              type="button"
              role={menu ? 'menuitem' : undefined}
              aria-label={`Delete saved theme ${theme.name}`}
              title="Delete the saved theme; the current look stays until you choose another"
              disabled={saving}
              onClick={() => void change('delete', undefined, theme.name)}
              className="flex h-6 w-6 max-md:h-8 max-md:w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100 max-md:opacity-100 hover:bg-muted hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <Trash2 aria-hidden="true" className="h-3.5 w-3.5" />
            </button>
          </div>
        })}
      </div>
    </div>}

    <div className="space-y-2 border-t border-border pt-2.5 text-xs">
      <div className="flex items-center justify-between gap-2 px-0.5">
        <span className="whitespace-nowrap text-muted-foreground">Text size</span>
        <div className="inline-flex h-7 items-center overflow-hidden rounded-md border border-border bg-background">
          <button
            type="button"
            aria-label="Decrease text size"
            disabled={saving || current.interface_size <= 12}
            onClick={() => typography({ interface_size: current.interface_size - 1 })}
            className="flex h-full w-7 items-center justify-center text-sm text-foreground hover:bg-muted disabled:opacity-35 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >−</button>
          <output aria-label="Text size" className="flex h-full min-w-10 items-center justify-center border-x border-border px-1.5 text-xs tabular-nums text-foreground">{current.interface_size}px</output>
          <button
            type="button"
            aria-label="Increase text size"
            disabled={saving || current.interface_size >= 20}
            onClick={() => typography({ interface_size: current.interface_size + 1 })}
            className="flex h-full w-7 items-center justify-center text-sm text-foreground hover:bg-muted disabled:opacity-35 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >+</button>
        </div>
      </div>
      <label className="flex items-center justify-between gap-2 px-0.5">
        <span className="shrink-0 whitespace-nowrap text-muted-foreground">Font</span>
        <span className="relative w-[148px] max-w-[65%] min-w-0">
          <select
            aria-label="Font"
            value={current.interface_font}
            disabled={saving}
            onChange={event => typography({ interface_font: event.target.value })}
            className="h-7 w-full appearance-none truncate rounded-md border border-border bg-background pl-2.5 pr-6 text-xs text-foreground hover:border-muted-foreground/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {INTERFACE_FONTS.filter(font => !fonts || fonts.includes(font.id)).map(font => <option key={font.id} value={font.id}>{font.label}</option>)}
          </select>
          <ChevronDown aria-hidden="true" className="pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 text-muted-foreground" />
        </span>
      </label>
    </div>
  </div>
}

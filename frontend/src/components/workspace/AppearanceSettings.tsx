import { useRef, useState } from 'react'
import { Download, Upload, RotateCcw } from 'lucide-react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { APPEARANCE_HOOKS, CODE_FONTS, COLOUR_ROLES, INTERFACE_FONTS, codeFontFamily, interfaceFontFamily, validateTheme, type AppearanceTheme, type ColourRole } from '../../stores/appearanceTheme'
import { Button } from '../ui/Button'

const inputClass = 'w-full rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const colorNames: Record<ColourRole, string> = { background: 'Page background', foreground: 'Text', card: 'Panels & menus', muted: 'Subtle surfaces', muted_foreground: 'Secondary text', border: 'Borders', accent: 'Action colour', sidebar: 'Sidebar' }
const colorDefaults = { light: { background: '#fcfcfc', foreground: '#262626', card: '#ffffff', muted: '#f5f5f5', muted_foreground: '#666666', border: '#e3e3e3', accent: '#2563eb', sidebar: '#f5f5f5' }, dark: { background: '#0d0f12', foreground: '#e4e7ec', card: '#15181c', muted: '#1e2228', muted_foreground: '#a0a8b5', border: '#292e35', accent: '#007acc', sidebar: '#1e2228' } }
export default function AppearanceSettings() {
  const store = useAppearanceStore()
  const theme = store.preview ?? store.current
  const [importError, setImportError] = useState('')
  const file = useRef<HTMLInputElement>(null)
  const update = (patch: Partial<AppearanceTheme>) => store.setPreview({ ...theme, ...patch })
  const color = (role: ColourRole, value: string | undefined) => {
    const colors = structuredClone(theme.colors)
    colors[theme.mode] ??= {}
    if (value) colors[theme.mode]![role] = value; else delete colors[theme.mode]![role]
    update({ colors })
  }
  const exportTheme = () => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(theme, null, 2)], { type: 'application/json' }))
    const link = document.createElement('a'); link.href = url; link.download = 'agentworks-theme.json'; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return <div className="max-w-4xl space-y-7">
    <p className="text-sm text-muted-foreground">Make AgentWorks yours. Fonts, colours and styling apply across the app, not just chat. Your appearance belongs to your sign-in on this connection; it never changes a Crew or an Artifact’s authored design.</p>
    {store.error && <div role="alert" className="rounded-md border border-destructive/30 p-3 text-sm text-destructive">{store.error} <button type="button" className="underline" onClick={() => void store.load(store.scope)}>Retry load</button></div>}
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card p-3">
      <label className="sr-only" htmlFor="saved-theme">Saved theme</label>
      <select id="saved-theme" className={`${inputClass} min-w-40 flex-1`} value="" onChange={event => { const selected = store.saved.find(item => item.name === event.target.value); if (selected) store.setPreview(structuredClone(selected)) }}>
        <option value="">Choose a saved theme…</option>
        {store.saved.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}
      </select>
      <Button variant="outline" size="sm" onClick={exportTheme}><Download className="h-3.5 w-3.5" />Export</Button>
      <Button variant="outline" size="sm" onClick={() => file.current?.click()}><Upload className="h-3.5 w-3.5" />Import</Button>
      <Button variant="ghost" size="sm" disabled={store.saving} onClick={() => void store.change('reset')}><RotateCcw className="h-3.5 w-3.5" />Reset</Button>
      <input ref={file} type="file" accept=".json,application/json" className="hidden" aria-label="Import theme file" onChange={async event => {
        const selected = event.target.files?.[0]; if (!selected) return
        try { if (selected.size > 100000) throw new Error('Choose a theme file below 100 KB.');store.setPreview(validateTheme(JSON.parse(await selected.text())));setImportError('') } catch (error) {setImportError(error instanceof Error ? error.message : 'Could not import theme.')}
        event.target.value = ''
      }} />
    </div>
    {importError && <p role="alert" className="text-sm text-destructive">{importError}</p>}
    <div className="grid gap-7 lg:grid-cols-2">
      <section className="space-y-4" aria-label="Typography">
        <h3 className="text-sm font-semibold">Typography</h3>
        <label className="block space-y-1.5 text-sm"><span>Interface font</span><select className={inputClass} value={theme.interface_font} onChange={event => update({ interface_font: event.target.value })}>{INTERFACE_FONTS.map(font => <option key={font.id} value={font.id}>{font.label}</option>)}</select></label>
        <label className="block space-y-1.5 text-sm"><span>Interface size</span><input className={inputClass} type="range" min="12" max="20" value={theme.interface_size} onChange={event => update({ interface_size: +event.target.value })} /><span className="text-xs text-muted-foreground">{theme.interface_size}px · menus, settings and conversations</span></label>
        <label className="block space-y-1.5 text-sm"><span>Code font</span><select className={inputClass} value={theme.code_font} onChange={event => update({ code_font: event.target.value })}>{CODE_FONTS.map(font => <option key={font.id} value={font.id}>{font.label}</option>)}</select></label>
        <label className="block space-y-1.5 text-sm"><span>Code size</span><input className={inputClass} type="range" min="10" max="20" value={theme.code_size} onChange={event => update({ code_size: +event.target.value })} /><span className="text-xs text-muted-foreground">{theme.code_size}px · code blocks and terminals</span></label>
      </section>
      <section className="space-y-4" aria-label="Theme colours">
        <div className="flex items-center justify-between gap-3"><h3 className="text-sm font-semibold">Colours</h3><div className="flex gap-1">{(['light','dark'] as const).map(mode => <button key={mode} type="button" aria-pressed={theme.mode === mode} onClick={() => update({ mode })} className={`rounded-md px-3 py-1.5 text-xs capitalize ${theme.mode === mode ? 'bg-muted text-foreground' : 'text-muted-foreground'}`}>{mode}</button>)}</div></div>
        <p className="text-xs text-muted-foreground">Light and Dark keep separate colours. Reset a colour to follow the default palette.</p>
        <div className="grid grid-cols-2 gap-3">{COLOUR_ROLES.map(role => <div key={role} className="flex items-center gap-2 rounded-md border border-border p-2"><label className="flex min-w-0 flex-1 items-center gap-2 text-xs"><input aria-label={colorNames[role]} type="color" value={theme.colors[theme.mode]?.[role] ?? colorDefaults[theme.mode][role]} onChange={event => color(role, event.target.value)} className="h-6 w-7 shrink-0 cursor-pointer bg-transparent" /><span>{colorNames[role]}</span></label>{theme.colors[theme.mode]?.[role] && <button type="button" aria-label={`Reset ${colorNames[role]}`} onClick={() => color(role,undefined)}><RotateCcw className="h-3 w-3 text-muted-foreground" /></button>}</div>)}</div>
      </section>
    </div>
    <section aria-label="Appearance preview" className="space-y-3 rounded-xl border border-border bg-card p-5">
      <p className="text-xs text-muted-foreground">Live preview · changes are not saved until you apply them</p>
      <div style={{ fontFamily: interfaceFontFamily(theme.interface_font) }}><p className="ml-auto w-fit rounded-xl bg-muted px-4 py-2">Can you review my changes?</p><p className="mt-4">I’ll check the changes and explain what needs attention.</p></div>
      <pre className="overflow-auto rounded-md bg-muted p-3" style={{ fontFamily: codeFontFamily(theme.code_font), fontSize: theme.code_size }}>{'const ready = changes.every(check => check.passed)\n// 0123456789 · { } [ ] != =>'}</pre>
    </section>
    <details className="space-y-3 rounded-lg border border-border p-4"><summary className="cursor-pointer text-sm font-medium">Advanced · custom CSS & agent styling</summary>
      <p className="text-xs text-muted-foreground">CSS applies after app styles and can override same-page elements it matches. Avoid .force-light and [data-report-theme] to preserve authored designs. Style the stable selectors below; generated utility classes may change. External resources are blocked; embedded data images are allowed. Agents can use settings action=appearance to read, apply, save or reset this same theme.</p>
      <textarea aria-label="Custom CSS" className={`${inputClass} min-h-40 font-mono`} maxLength={20000} spellCheck={false} value={theme.css} onChange={event => update({ css: event.target.value })} placeholder={'[data-chat-composer-card] { border-radius: 20px; }'} />
      <p className="text-xs text-muted-foreground">{new TextEncoder().encode(theme.css).length}/20,000 bytes</p>
      <div className="flex flex-wrap gap-2">{APPEARANCE_HOOKS.map(hook => <code key={hook} className="rounded bg-muted px-1.5 py-1 text-xs">{hook}</code>)}</div>
    </details>
    <div className="flex flex-wrap items-end gap-3 border-t border-border pt-4">
      <label className="min-w-40 flex-1 space-y-1 text-xs"><span>Theme name</span><input aria-label="Theme name" className={inputClass} maxLength={60} value={theme.name} onChange={event => update({ name: event.target.value })} /></label>
      <Button disabled={store.saving} onClick={() => void store.change('apply',theme)}>{store.saving ? 'Saving…' : 'Apply'}</Button>
      <Button variant="outline" disabled={store.saving || !theme.name.trim()} onClick={() => void store.change('save',theme)}>Save theme</Button>
      {store.preview && <Button variant="ghost" onClick={() => store.setPreview(null)}>Discard preview</Button>}
    </div>
    {store.saved.length > 0 && <section className="space-y-2" aria-label="Saved themes"><h3 className="text-sm font-medium">Saved themes</h3>{store.saved.map(item => <div key={item.name} className="flex items-center gap-3 rounded-md border border-border px-3 py-2"><span className="flex-1 text-sm">{item.name}</span><Button variant="ghost" size="sm" disabled={store.saving} onClick={() => void store.change('apply',item)}>Apply</Button><Button variant="ghost" size="sm" disabled={store.saving} onClick={() => void store.change('delete',undefined,item.name)}>Remove</Button></div>)}</section>}
  </div>
}

import { useEffect, useState } from 'react'
import { X, Plus, Trash2, Save, Loader2 } from 'lucide-react'
import { agentApi } from '../../../services/api'
import type { VariablesManifest } from '../../../services/api-types'

interface VariablesSidebarProps {
  workspacePath: string | null
  relayMode?: boolean
  onClose: () => void
  onUpdate?: (manifest: VariablesManifest) => void
  showChatArea?: boolean
  embedded?: boolean
}

export function VariablesSidebar({ workspacePath, relayMode = false, onClose, onUpdate, showChatArea = false, embedded = false }: VariablesSidebarProps) {
  const [manifest, setManifest] = useState<VariablesManifest | null>(null)
  const [preset, setPreset] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [blocked, setBlocked] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    let current = true
    if (!workspacePath) { setLoading(false); return }
    setLoading(true)
    void agentApi.getVariableGroups(workspacePath).then(response => {
      if (!current) return
      if (!response.success) throw new Error(response.error || 'Could not load inputs.')
      const data = response.manifest ?? { objective: '', extraction_date: '', variables: [] }
      if (relayMode && (data.groups?.length ?? 0) > 1) {
        setManifest({ ...data, variables: [...data.variables, ...(!data.variables.some(variable => variable.name === 'INPUT') ? [{ name: 'INPUT', value: '{}', description: '' }] : [])] })
        setBlocked(true)
        throw new Error('This legacy Flow has multiple variable groups. Ask the Builder to choose the configuration values to keep and remove the groups before editing here.')
      }
      const normalized = { ...data, variables: (data.variables ?? []).map(variable => ({ ...variable })) }
      if (relayMode) {
        for (const [name, value] of Object.entries(data.groups?.[0]?.values ?? {})) {
          const variable = normalized.variables.find(variable => variable.name === name)
          if (variable) variable.value = value
          else normalized.variables.push({ name, value, description: '' })
        }
        delete normalized.groups
        if (!normalized.variables.some(variable => variable.name === 'INPUT')) normalized.variables.push({ name: 'INPUT', value: '{}', description: 'Sample input object for testing the program.' })
      }
      setManifest(normalized)
      setPreset(null)
      setDirty(false)
    }).catch(cause => { if (current) { setError(cause instanceof Error ? cause.message : 'Could not load inputs.'); setBlocked(true) } })
      .finally(() => { if (current) setLoading(false) })
    return () => { current = false }
  }, [workspacePath, relayMode])
  useEffect(() => {
    if (embedded) return
    const opener = document.activeElement as HTMLElement | null
    const close = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', close)
    return () => { document.removeEventListener('keydown', close); opener?.focus?.() }
  }, [embedded, onClose])
  function update(next: VariablesManifest) { setManifest(next); setDirty(true) }
  async function save() {
    if (!manifest || !workspacePath) return
    setSaving(true); setError('')
    try {
      if (relayMode) {
        const sample = manifest.variables.find(variable => variable.name === 'INPUT')?.value ?? '{}'
        const parsed: unknown = JSON.parse(sample)
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('Sample INPUT must be a JSON object.')
      }
      const response = await agentApi.updateVariableGroups(workspacePath, manifest)
      if (!response.success) throw new Error(response.message || 'Could not save inputs.')
      setDirty(false); onUpdate?.(manifest)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not save inputs.') }
    finally { setSaving(false) }
  }
  const saved = preset === null ? null : manifest?.groups?.[preset]
  const content = <>
    <div className="flex items-center gap-2 border-b p-4">
      <h3 className="flex-1 text-sm font-medium">{relayMode ? 'Configuration' : 'Inputs'}</h3>
      <button className="flex items-center gap-2 rounded border px-3 py-1.5 text-sm disabled:opacity-50" disabled={!dirty || saving || blocked} onClick={() => void save()}>
        {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />} Save
      </button>
      {!embedded && <button onClick={onClose} aria-label="Close inputs"><X className="h-4 w-4" /></button>}
    </div>
    <div className="min-h-0 flex-1 space-y-5 overflow-auto p-4">
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {loading ? <p className="text-sm text-muted-foreground">Loading inputs…</p> : manifest && <>
        {!relayMode && <div className="flex flex-wrap items-center gap-2">
          <label className="text-sm">Values <select aria-label="Edit input values" className="ml-2 rounded border bg-background p-2" value={preset ?? ''} onChange={event => setPreset(event.target.value === '' ? null : Number(event.target.value))}>
            <option value="">Workflow defaults</option>
            {(manifest.groups ?? []).map((group, index) => <option key={index} value={index}>{group.name}</option>)}
          </select></label>
          <button className="rounded border px-3 py-2 text-sm" disabled={blocked} onClick={() => {
            const groups = manifest.groups ?? []
            let number = groups.length + 1
            while (groups.some(group => group.name === `Saved inputs ${number}`)) number++
            update({ ...manifest, groups: [...groups, { name: `Saved inputs ${number}`, values: {} }] })
            setPreset(groups.length)
          }}>Save another set</button>
        </div>}
        {saved && preset !== null && <div className="flex items-center gap-2">
          <input aria-label="Saved input name" className="min-w-0 flex-1 rounded border bg-background p-2 text-sm" value={saved.name} onChange={event => update({ ...manifest, groups: manifest.groups?.map((group, index) => index === preset ? { ...group, name: event.target.value } : group) })} />
          <button aria-label="Delete saved inputs" onClick={() => { update({ ...manifest, groups: manifest.groups?.filter((_, index) => index !== preset) }); setPreset(null) }}><Trash2 className="h-4 w-4" /></button>
        </div>}
        <p className="text-sm text-muted-foreground">{saved ? 'Only changed values are saved here. Other values use the workflow defaults.' : relayMode ? 'Configuration values for this program. Keep credentials in Vault.' : 'Inputs are optional. A run uses defaults, then a selected saved set, then any values supplied for that run. Keep credentials in Vault.'}</p>
        {!manifest.variables.length && <p className="text-sm text-muted-foreground">No inputs needed. This workflow can run as it is.</p>}
        {manifest.variables.map((variable, index) => <div key={index} className="space-y-2 border-b pb-4">
          {saved ? <label className="block text-sm font-medium" htmlFor={`input-value-${index}`}>{variable.name}</label> : <div className="flex items-center gap-2">
            <input disabled={blocked} aria-label={`Input ${index + 1} name`} className="min-w-0 flex-1 rounded border bg-background p-2 text-sm" value={variable.name} onChange={event => {
              const name = event.target.value
              update({ ...manifest, variables: manifest.variables.map((entry, i) => i === index ? { ...entry, name } : entry), groups: manifest.groups?.map(group => {
                const values = { ...group.values }
                if (Object.hasOwn(values, variable.name)) { values[name] = values[variable.name]; delete values[variable.name] }
                return { ...group, values }
              }) })
            }} />
            <button aria-label={`Delete input ${variable.name}`} onClick={() => update({ ...manifest, variables: manifest.variables.filter((_, i) => i !== index), groups: manifest.groups?.map(group => { const values = { ...group.values }; delete values[variable.name]; return { ...group, values } }) })}><Trash2 className="h-4 w-4" /></button>
          </div>}
          <textarea id={`input-value-${index}`} disabled={blocked} aria-label={relayMode ? variable.name === 'INPUT' ? 'Sample INPUT JSON' : variable.name : `${variable.name || 'New input'} value`} className="min-h-16 w-full rounded border bg-background p-2 text-sm" value={saved ? saved.values[variable.name] ?? variable.value ?? '' : variable.value ?? ''} onChange={event => {
            if (saved) update({ ...manifest, groups: manifest.groups?.map((group, i) => i === preset ? { ...group, values: { ...group.values, [variable.name]: event.target.value } } : group) })
            else update({ ...manifest, variables: manifest.variables.map((entry, i) => i === index ? { ...entry, value: event.target.value } : entry) })
          }} />
          {saved && Object.hasOwn(saved.values, variable.name) && <button className="text-xs text-muted-foreground underline" onClick={() => update({ ...manifest, groups: manifest.groups?.map((group, i) => { if (i !== preset) return group; const values = { ...group.values }; delete values[variable.name]; return { ...group, values } }) })}>Use workflow default</button>}
          {variable.description && <p className="text-xs text-muted-foreground">{variable.description}</p>}
        </div>)}
        {!saved && <button className="flex items-center gap-2 rounded border px-3 py-2 text-sm" disabled={blocked} onClick={() => update({ ...manifest, variables: [...manifest.variables, { name: '', value: '', description: '' }] })}><Plus className="h-4 w-4" /> Add input</button>}
      </>}
    </div>
  </>
  if (embedded) return <div className="flex h-full min-h-0 flex-col">{content}</div>
  return <aside role="dialog" aria-label="Workflow inputs" className={`fixed bottom-0 right-0 top-0 flex w-[min(28rem,100vw)] flex-col border-l bg-background shadow-xl ${showChatArea ? 'z-30' : 'z-50'}`}>{content}</aside>
}
export default VariablesSidebar

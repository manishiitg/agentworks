import { useState } from 'react'
import { Fingerprint } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { crewTemplates, crewTemplateBrowseCategory, crewTemplateBrowsePath, matchesCrewTemplateSearch, type CrewTemplateId } from './crewTemplates'

export function CrewTemplatePicker({ projectTemplates, onInstallTemplate, onError }: {
  projectTemplates: Array<{ id: string; version: number }>
  onInstallTemplate: (id: CrewTemplateId) => Promise<void>
  onError: (message: string | null) => void
}) {
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState('all')
  const [installingId, setInstallingId] = useState<CrewTemplateId | null>(null)
  const uninstalled = crewTemplates.filter(item => !projectTemplates.some(installed => installed.id === item.id))
  const categories = [...new Set(uninstalled.map(crewTemplateBrowseCategory))].sort((a, b) => a.localeCompare(b))
  const activeCategory = categories.includes(category) ? category : 'all'
  const matches = uninstalled.filter(item =>
    (activeCategory === 'all' || crewTemplateBrowseCategory(item) === activeCategory)
    && matchesCrewTemplateSearch(item, search),
  )

  const clearFilters = () => {
    setSearch('')
    setCategory('all')
  }

  const install = (id: CrewTemplateId) => {
    setInstallingId(id)
    onError(null)
    void onInstallTemplate(id)
      .catch(cause => onError(cause instanceof Error ? cause.message : 'Could not install template.'))
      .finally(() => setInstallingId(null))
  }

  return (
    <SettingsCard
      icon={<Fingerprint aria-hidden="true" className="h-4 w-4 text-primary" />}
      title="Crew templates"
      description="Install reusable skills and setup checklists. Installation starts setup; complete the required checks in chat. Existing files and progress are preserved."
    >
      {projectTemplates.length ? <div className="space-y-2">{projectTemplates.map(installed => {
        const template = crewTemplates.find(item => item.id === installed.id && item.version === installed.version)
        return <div key={installed.id} className="rounded-md border border-border p-2"><p className="text-sm font-semibold text-foreground">{template?.icon} {template?.name || installed.id}</p><p className="text-xs text-muted-foreground">{template ? `${crewTemplateBrowsePath(template)} · ` : ''}Installed · Version {installed.version} · Check setup progress in chat</p></div>
      })}</div> : <p className="text-xs text-muted-foreground">No templates installed yet.</p>}
      <div className="mt-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_12rem]">
        <Input aria-label="Search Crew templates" placeholder="Search templates" value={search} onChange={event => setSearch(event.target.value)} />
        <select aria-label="Filter template category" value={activeCategory} onChange={event => setCategory(event.target.value)} className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm text-foreground outline-none focus:border-primary focus:ring-2 focus:ring-primary/15">
          <option value="all">All categories ({uninstalled.length})</option>
          {categories.map(item => <option key={item} value={item}>{item} ({uninstalled.filter(template => crewTemplateBrowseCategory(template) === item).length})</option>)}
        </select>
      </div>
      <div className="mt-2 flex items-center justify-between gap-2 text-xs text-muted-foreground" role="status">
        <span>{matches.length === 0 ? 'No results' : `${matches.length} ${matches.length === 1 ? 'result' : 'results'}`}</span>
        {search || activeCategory !== 'all' ? <button type="button" onClick={clearFilters} className="font-medium text-primary hover:underline">Clear filters</button> : null}
      </div>
      <div className="mt-2 max-h-80 space-y-2 overflow-y-auto">
        {matches.map(template => <div key={template.id} data-testid={`identity-template-${template.id}`} className="flex items-center gap-2 rounded-md border border-border p-2">
          <div className="min-w-0 flex-1"><p className="text-sm font-semibold text-foreground">{template.icon} {template.name}</p><p className="text-xs text-muted-foreground">{crewTemplateBrowsePath(template)} · {template.firstResult}</p></div>
          <Button type="button" disabled={Boolean(installingId)} onClick={() => install(template.id)}>{installingId === template.id ? 'Adding…' : 'Add'}</Button>
        </div>)}
        {matches.length === 0 ? <div className="rounded-md border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground">No templates match these filters.</div> : null}
      </div>
    </SettingsCard>
  )
}

import { useEffect, useMemo, useState } from 'react'
import { Activity, ArrowLeft, BookMarked, CheckCircle2, ChevronDown, ChevronRight, CircleDot, FolderTree, Layers3, Loader2, PackageCheck, Search, Settings2, Wrench } from 'lucide-react'
import { isNewerPlaybookVersion, playbookBrowseCategory, playbookBrowsePath, PLAYBOOK_CATALOG, PLAYBOOK_CATEGORIES, type PlaybookCatalogItem } from './playbookCatalog'
import { playbooksApi } from '../../api/playbooks'
import type { InstalledPlaybook } from '../../services/api-types'
import { useCanWriteWorkflow, READ_ONLY_TITLE } from '../../hooks/useCanWriteWorkflow'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { AskAIButton } from '../workflow/AskAIButton'
import { WorkspaceViewTabs } from '../workflow/WorkspaceViewTabs'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { agentApi } from '../../services/api'
import { responseContent } from '../../utils/plannerFiles'
import { parsePlaybookSetupProgress, type PlaybookSetupProgress } from './playbookSetupProgress'
import { playbookSetupMessage } from './playbookSetupMessage'

type PlaybooksPanelProps = {
  workspacePath: string | null
}

type Tab = 'installed' | 'catalog'

export default function PlaybooksPanel({ workspacePath }: PlaybooksPanelProps) {
  const [tab, setTab] = usePersistentTab<Tab>('agentworks.tab.playbooks', 'catalog', ['installed', 'catalog'])
  const [query, setQuery] = useState('')
  const [expandedCategories, setExpandedCategories] = useState<Set<string>>(() => new Set(PLAYBOOK_CATEGORIES))
  const [selected, setSelected] = useState<PlaybookCatalogItem | null>(null)
  const [catalog, setCatalog] = useState<readonly PlaybookCatalogItem[]>(PLAYBOOK_CATALOG)
  const [installed, setInstalled] = useState<InstalledPlaybook[]>([])
  const [loading, setLoading] = useState(Boolean(workspacePath))
  const [installing, setInstalling] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [setupProgress, setSetupProgress] = useState<PlaybookSetupProgress | null>(null)
  const [setupProgressError, setSetupProgressError] = useState(false)
  const canWrite = useCanWriteWorkflow(workspacePath)

  useEffect(() => {
    let active = true
    setLoading(Boolean(workspacePath))
    setError(null)
    const requests: [Promise<PlaybookCatalogItem[]>, Promise<InstalledPlaybook[]>] = [
      playbooksApi.list(),
      workspacePath ? playbooksApi.listInstalled(workspacePath) : Promise.resolve([]),
    ]
    void Promise.all(requests).then(([nextCatalog, nextInstalled]) => {
      if (!active) return
      if (nextCatalog.length > 0) setCatalog(nextCatalog)
      setInstalled(nextInstalled)
    }).catch(cause => {
      if (active) setError(cause instanceof Error ? cause.message : 'Unable to load playbooks')
    }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [workspacePath])

  const categories = useMemo(() => [...new Set(catalog.map(playbookBrowseCategory))], [catalog])

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return catalog
      .filter(playbook => !needle || `${playbook.title} ${playbook.description} ${playbookBrowsePath(playbook)} ${(playbook.setupInputs || []).map(input => input.label).join(' ')} ${(playbook.outputs || []).join(' ')}`.toLowerCase().includes(needle))
      .sort((a, b) => playbookBrowseCategory(a).localeCompare(playbookBrowseCategory(b)) || a.category.localeCompare(b.category) || a.order - b.order)
  }, [catalog, query])

  const visibleGroups = useMemo(() => categories
    .map(groupCategory => ({ category: groupCategory, playbooks: visible.filter(playbook => playbookBrowseCategory(playbook) === groupCategory) }))
    .filter(group => group.playbooks.length > 0), [categories, visible])
  const visibleProposalCount = visible.filter(item => item.agentSlots?.length).length
  const visibleGuideCount = visible.length - visibleProposalCount

  const toggleCategory = (groupCategory: string) => {
    setExpandedCategories(current => {
      const next = new Set(current)
      if (next.has(groupCategory)) next.delete(groupCategory)
      else next.add(groupCategory)
      return next
    })
  }

  const installedSelection = selected ? installed.find(item => item.id === selected.id) : undefined
  const selectedId = selected?.id
  const setupSkillName = selected?.setupChecks?.length ? installedSelection?.skill_name : undefined
  const setupVersion = selected?.setupChecks?.length ? installedSelection?.version : undefined
  const selectedChecks = selected?.setupChecks
  useEffect(() => {
    if (!workspacePath || !selectedId || !setupSkillName || !setupVersion || !selectedChecks?.length) {
      setSetupProgress(null)
      setSetupProgressError(false)
      return
    }
    let active = true
    const refresh = async () => {
      try {
        const filePath = `${workspacePath}/skills/${setupSkillName}/SETUP.json`
        const response = await agentApi.getPlannerFileContent(filePath)
        const content = responseContent(response)?.content || ''
        const progress = parsePlaybookSetupProgress(content, selectedId, setupVersion, selectedChecks)
        if (active) {
          setSetupProgress(progress)
          setSetupProgressError(!progress)
        }
      } catch {
        if (active) {
          setSetupProgress(null)
          setSetupProgressError(true)
        }
      }
    }
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 10000)
    return () => { active = false; window.clearInterval(timer) }
  }, [workspacePath, selectedId, setupSkillName, setupVersion, selectedChecks])
  const updateAvailable = Boolean(installedSelection && selected && isNewerPlaybookVersion(selected.version, installedSelection.version))
  const applicableChangelog = installedSelection && selected
    ? (selected.changelog || []).filter(entry =>
      isNewerPlaybookVersion(entry.version, installedSelection.version)
      && !isNewerPlaybookVersion(entry.version, selected.version))
    : []
  const installSelected = async () => {
    if (!workspacePath || !selected || !canWrite) return
    setInstalling(true)
    setError(null)
    try {
      const result = await playbooksApi.install(workspacePath, selected.id)
      setInstalled(current => [...current.filter(item => item.id !== result.id), result])
      await useWorkflowManifestStore.getState().refreshWorkflows()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Unable to install playbook')
    } finally {
      setInstalling(false)
    }
  }

  if (selected) {
    const setupInputs = selected.setupInputs || []
    const requiredCapabilities = selected.requiredCapabilities || []
    const recommendedTools = selected.recommendedTools || []
    const pulseFocus = (selected.pulseFocus || []).filter(focus => focus.module === 'strategic_review')
    const outputs = selected.outputs || []
    const agentSlots = selected.agentSlots || []
    const handoffs = selected.handoffs || []
    const setupChecks = selected.setupChecks || []
    const isCrewProposal = agentSlots.length > 0
    return (
      <div className="min-h-0 flex-1 overflow-y-auto">
        <Button type="button" variant="link" size="sm" onClick={() => setSelected(null)} className="mb-4">
          <ArrowLeft className="h-3.5 w-3.5" /> Back to catalog
        </Button>
        <div className="rounded-xl border border-border bg-card p-5">
          <div className="flex items-start gap-3">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary"><BookMarked className="h-5 w-5" /></div>
            <div className="min-w-0 flex-1">
              <div className="text-xs font-medium text-primary">AgentWorks / Agentic Engineering Platform / {playbookBrowsePath(selected)}</div>
              <h3 className="mt-1 text-lg font-semibold text-foreground">{selected.title}</h3>
              <span className="mt-2 inline-flex rounded-full border border-border bg-muted/30 px-2 py-0.5 text-[11px] font-medium text-muted-foreground">{isCrewProposal ? 'Crew automation proposal' : 'Workflow guide'}</span>
              <p className="mt-2 text-sm leading-6 text-muted-foreground">{selected.description}</p>
            </div>
            <div className="flex shrink-0 flex-col items-end gap-1"><span className="rounded-full border border-border px-2 py-1 text-[11px] text-muted-foreground">v{selected.version}</span>{selected.teamScope === 'small_team' && <span className="rounded-full bg-primary/10 px-2 py-1 text-[10px] font-medium text-primary">Small team</span>}</div>
          </div>
          <div className="mt-5 grid gap-3 sm:grid-cols-2">
            <div className="rounded-lg border border-border bg-muted/20 p-3"><div className="flex items-center gap-2 text-sm font-medium"><Settings2 className="h-4 w-4 text-muted-foreground" /> Setup inputs</div><p className="mt-1 text-xs text-muted-foreground">{selected.inputCount} inputs guide adaptation to the company and workflow.</p></div>
            <div className="rounded-lg border border-border bg-muted/20 p-3"><div className="flex items-center gap-2 text-sm font-medium"><Wrench className="h-4 w-4 text-muted-foreground" /> Recommended tools</div><p className="mt-1 text-xs text-muted-foreground">{selected.toolCount} built-in, CLI, MCP, or skill recommendations.</p></div>
          </div>
          {(setupInputs.length > 0 || requiredCapabilities.length > 0) && (
            <section className="mt-5 rounded-lg border border-border p-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-foreground"><Layers3 className="h-4 w-4 text-primary" /> What this playbook covers</div>
              {setupInputs.length > 0 && (
                <div className="mt-3">
                  <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Setup areas</div>
                  <div className="mt-2 grid gap-2 sm:grid-cols-2">
                    {setupInputs.map(input => (
                      <div key={input.id} className="flex items-start gap-2 text-xs leading-5 text-foreground/90">
                        <CircleDot className="mt-1 h-3 w-3 shrink-0 text-primary/70" />
                        <span>{input.label}{!input.required && <span className="ml-1 text-muted-foreground">(optional)</span>}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              {requiredCapabilities.length > 0 && (
                <div className="mt-4">
                  <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Capabilities to set up</div>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {requiredCapabilities.map(capability => <span key={capability} className="rounded-full border border-border bg-muted/30 px-2 py-1 text-[11px] text-muted-foreground">{capability.replaceAll('_', ' ')}</span>)}
                  </div>
                </div>
              )}
            </section>
          )}
          {agentSlots.length > 0 && (
            <section className="mt-4 rounded-lg border border-primary/20 bg-primary/[0.04] p-4" aria-label="Proposed Crew team">
              <div className="flex items-center gap-2 text-sm font-semibold text-foreground"><Layers3 className="h-4 w-4 text-primary" /> Proposed Crew team</div>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">Builder will inspect your existing Crews, propose which specialists to reuse or create, and set up the reviewed team in chat. Choosing this Playbook creates no Crew members.</p>
              <div className="mt-3 grid gap-2 sm:grid-cols-2">
                {agentSlots.map(slot => <div key={slot.id} className="rounded-lg border border-border bg-background p-3"><div className="flex items-center justify-between gap-2"><span className="text-xs font-semibold capitalize text-foreground">{slot.id.replaceAll('-', ' ')}</span><span className="text-[10px] text-muted-foreground">{slot.required ? 'Required' : 'Optional'}</span></div><p className="mt-1 text-xs text-muted-foreground">{slot.agent_playbook_id.replaceAll('-', ' ')}{slot.accepts?.length ? ` or ${slot.accepts.map(id => id.replaceAll('-', ' ')).join(', ')}` : ''}</p><p className="mt-1 text-[11px] text-muted-foreground">Output: {slot.output}</p></div>)}
              </div>
              {handoffs.length > 0 && <div className="mt-3 border-t border-border pt-3 text-xs text-muted-foreground"><span className="font-medium text-foreground">Handoffs:</span> {handoffs.map(handoff => `${handoff.from} → ${handoff.to} (${handoff.artifact_type})`).join(' · ')}<p className="mt-2">Builder must test a blocking validator for each chosen handoff before the next Crew uses its output.</p></div>}
            </section>
          )}
          {setupChecks.length > 0 && (
            <section className="mt-4 rounded-lg border border-border p-4" aria-label="Setup checks">
              <div className="flex items-center gap-2 text-sm font-semibold text-foreground"><CheckCircle2 className="h-4 w-4 text-primary" /> Setup checks</div>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">{installedSelection && setupProgress ? `${setupProgress.completed.length} of ${setupChecks.length} checks recorded with evidence. Builder updates this as you set up the actual team and test a first run.` : 'Builder verifies these with your actual team and first run. This is a setup plan, not a completed checklist.'}</p>
              {installedSelection && setupProgressError && <p className="mt-2 text-xs text-amber-700 dark:text-amber-300">Saved setup progress is unavailable. Update this proposal or ask Builder to inspect its setup file.</p>}
              <div className="mt-3 grid gap-2 sm:grid-cols-2">{setupChecks.map(check => <div key={check} className="flex items-center gap-2 rounded-md bg-muted/30 px-2.5 py-2 text-xs text-foreground/90">{setupProgress?.completed.includes(check) ? <CheckCircle2 className="h-3 w-3 shrink-0 text-emerald-600" /> : <CircleDot className="h-3 w-3 shrink-0 text-muted-foreground" />}{check.replaceAll('_', ' ')}</div>)}</div>
            </section>
          )}
          {outputs.length > 0 && (
            <section className="mt-4 rounded-lg border border-border p-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-foreground"><PackageCheck className="h-4 w-4 text-primary" /> What you’ll get</div>
              <div className="mt-3 grid gap-2 sm:grid-cols-2">
                {outputs.map(output => <div key={output} className="flex items-start gap-2 text-xs leading-5 text-foreground/90"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-600" /><span>{output}</span></div>)}
              </div>
            </section>
          )}
          {pulseFocus.length > 0 && (
            <section className="mt-4 rounded-lg border border-border p-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-foreground"><Activity className="h-4 w-4 text-primary" /> Strategic review focus</div>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">A goal-specific lens for Strategic Review. Technical and Architecture Review choose scope from current evidence and structural risk.</p>
              <div className="mt-3 grid gap-2">
                {pulseFocus.map(focus => (
                  <div key={focus.module} className="rounded-md bg-muted/30 px-3 py-2">
                    <div className="text-xs font-medium text-foreground">{focus.label}</div>
                    <ul className="mt-1 space-y-1 text-xs leading-5 text-foreground/90">
                      {focus.focus_areas.map(area => <li key={area}>• {area}</li>)}
                    </ul>
                    {focus.review_when.length > 0 && <p className="mt-2 text-[11px] leading-5 text-muted-foreground"><span className="font-medium text-foreground/80">Review when:</span> {focus.review_when.join('; ')}</p>}
                  </div>
                ))}
              </div>
            </section>
          )}
          {recommendedTools.length > 0 && (
            <section className="mt-4 rounded-lg border border-border p-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-foreground"><Wrench className="h-4 w-4 text-primary" /> Recommended tools</div>
              <div className="mt-3 space-y-2">
                {recommendedTools.map(tool => (
                  <div key={tool.id} className="rounded-md bg-muted/30 px-3 py-2">
                    <div className="flex flex-wrap items-center gap-2"><span className="text-xs font-medium text-foreground">{tool.name}</span><span className="rounded border border-border px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">{tool.type}</span></div>
                    <p className="mt-1 text-xs leading-5 text-muted-foreground">{tool.purpose}</p>
                  </div>
                ))}
              </div>
            </section>
          )}
          {error && <p className="mt-4 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive">{error}</p>}
          <div className="mt-5 rounded-lg border border-dashed border-border p-4">
            <div className="flex items-center gap-2 text-sm font-medium"><CheckCircle2 className="h-4 w-4 text-muted-foreground" /> Setup with Builder</div>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">{isCrewProposal ? 'Using this proposal saves its guidance in the workflow. Continue in Builder chat to inspect existing Crews, review a concrete team and plan, and create or bind agents. Choosing the proposal starts no Crew, schedule, or run.' : 'Using this guide saves its instructions in the workflow for Builder chat. This package has no predefined Crew team or tracked setup checklist. Builder first inspects the current workflow, shows what can be reused and what is missing, then asks focused questions before proposing changes. Installing guidance is not approval to edit or run the workflow.'}</p>
            {installedSelection ? (
              <>
                <div className="mt-3 flex flex-wrap items-center gap-2">
                  <span className="inline-flex items-center gap-1.5 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-2 text-xs font-medium text-emerald-700 dark:text-emerald-300"><CheckCircle2 className="h-3.5 w-3.5" /> {isCrewProposal ? 'Proposal saved' : 'Workflow guide saved'} v{installedSelection.version} · {installedSelection.status}</span>
                  <AskAIButton workspacePath={workspacePath} label="Continue setup in Builder" message={playbookSetupMessage(selected, installedSelection.skill_name)} className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground hover:bg-primary/90" />
                </div>
                {updateAvailable && (
                  <div className="mt-3 rounded-md border border-amber-500/30 bg-amber-500/10 p-3">
                    <div className="text-xs font-semibold text-amber-800 dark:text-amber-200">Update available · v{selected.version}</div>
                    {applicableChangelog.length > 0 && (
                      <div className="mt-2 rounded-md border border-amber-500/20 bg-background/70 p-2.5">
                        <div className="text-xs font-medium text-foreground">What changed</div>
                        <ul className="mt-1 space-y-1 text-xs leading-5 text-muted-foreground">
                          {applicableChangelog.map(entry => <li key={entry.version}><span className="font-medium text-foreground">v{entry.version}:</span> {entry.summary}</li>)}
                        </ul>
                      </div>
                    )}
                    <p className="mt-1 text-xs leading-5 text-muted-foreground">Updating refreshes the installed guidance and marks its setup as draft. Builder should review the new instructions against the current workflow and revalidate affected routes. Workflow steps, goals, metrics, thresholds, schedules, tools, and customer preferences are not changed automatically.</p>
                    <Button type="button" variant="outline" size="sm" disabled={!canWrite || installing} title={!canWrite ? READ_ONLY_TITLE : undefined} onClick={() => void installSelected()} className="mt-2">{installing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{installing ? 'Updating…' : 'Update playbook'}</Button>
                  </div>
                )}
              </>
            ) : (
              <Button type="button" size="sm" disabled={!workspacePath || !canWrite || installing} title={!canWrite ? READ_ONLY_TITLE : undefined} onClick={() => void installSelected()} className="mt-3">{installing && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{installing ? 'Installing…' : isCrewProposal ? 'Use proposal' : 'Use workflow guide'}</Button>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="shrink-0 border-b border-border">
        <WorkspaceViewTabs
          value={tab}
          onChange={(value: string) => setTab(value as Tab)}
          options={[
            { value: 'installed', label: 'Installed', count: installed.length },
            { value: 'catalog', label: 'Catalog', count: catalog.length },
          ]}
          ariaLabel="Workflow playbooks"
        />
      </div>

      {loading ? <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading playbooks…</div> : tab === 'installed' ? (
        installed.length > 0 ? <div className="mt-3 min-h-0 flex-1 space-y-2 overflow-y-auto">{installed.map(item => { const latest = catalog.find(value => value.id === item.id); const hasUpdate = Boolean(latest && isNewerPlaybookVersion(latest.version, item.version)); return <button key={item.id} type="button" onClick={() => { if (latest) setSelected(latest) }} className="flex w-full items-center gap-3 rounded-lg border border-border bg-card p-3 text-left hover:border-primary/40"><div className="flex h-8 w-8 items-center justify-center rounded-md bg-emerald-500/10 text-emerald-600"><CheckCircle2 className="h-4 w-4" /></div><div className="min-w-0 flex-1"><div className="flex items-center gap-2"><span className="truncate text-sm font-medium">{item.title}</span>{hasUpdate && <span className="shrink-0 rounded-full bg-amber-500/15 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">Update available</span>}</div><div className="mt-0.5 text-xs text-muted-foreground">{playbookBrowsePath(item)} · v{item.version} · {item.status}{hasUpdate ? ` · latest v${latest?.version}` : ''}</div></div><ChevronRight className="h-4 w-4 text-muted-foreground" /></button>})}</div> : (
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-6 text-center">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-muted text-muted-foreground"><BookMarked className="h-5 w-5" /></div>
          <h3 className="mt-3 text-sm font-semibold text-foreground">No playbooks installed</h3>
          <p className="mt-1 max-w-sm text-xs leading-5 text-muted-foreground">Choose a playbook from the catalog to guide this workflow’s plan, capabilities, and dashboard.</p>
          <Button type="button" size="sm" onClick={() => setTab('catalog')} className="mt-4">Browse catalog</Button>
        </div>)
      ) : (
        <>
          <div className="mt-3 shrink-0 space-y-3">
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input value={query} onChange={event => setQuery(event.target.value)} placeholder="Search playbooks" aria-label="Search playbooks" className="pl-9" />
            </div>
            <div className="rounded-lg border border-border bg-muted/20 px-3 py-2.5" aria-label="Playbook catalog hierarchy">
              <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground"><FolderTree className="h-4 w-4" /> AgentWorks</div>
              <div className="ml-2 mt-2 border-l border-border pl-4 text-xs font-semibold text-foreground">Agentic Engineering Platform <span className="ml-1 font-normal text-muted-foreground">{visible.length} small-team {visible.length === 1 ? 'playbook' : 'playbooks'} · {visibleProposalCount} Crew {visibleProposalCount === 1 ? 'proposal' : 'proposals'} · {visibleGuideCount} Workflow {visibleGuideCount === 1 ? 'guide' : 'guides'}</span></div>
            </div>
          </div>
          <div className="mt-3 min-h-0 flex-1 overflow-y-auto">
            {visible.length === 0 ? <p className="py-10 text-center text-sm text-muted-foreground">No playbooks match this search.</p> : (
              <div className="space-y-3">
                {visibleGroups.map(group => {
                  const expanded = query.trim() !== '' || expandedCategories.has(group.category)
                  return (
                    <section key={group.category} className="rounded-lg border border-border bg-card/50">
                      <button type="button" aria-expanded={expanded} onClick={() => toggleCategory(group.category)} className="flex w-full items-center gap-2 px-3 py-2.5 text-left hover:bg-muted/30">
                        {expanded ? <ChevronDown className="h-4 w-4 text-muted-foreground" /> : <ChevronRight className="h-4 w-4 text-muted-foreground" />}
                        <span className="flex-1 text-sm font-semibold text-foreground">{group.category}</span>
                        <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">{group.playbooks.length}</span>
                      </button>
                      {expanded && (
                        <div className="ml-5 space-y-2 border-l border-border px-3 pb-3">
                          {group.playbooks.map(playbook => (
                            <div key={playbook.id} className="group flex w-full items-start gap-3 rounded-lg border border-border bg-card p-3 transition-colors hover:border-primary/40 hover:bg-muted/20">
                              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground group-hover:text-primary"><BookMarked className="h-4 w-4" /></div>
                              <div className="min-w-0 flex-1">
                                <div className="flex flex-wrap items-center gap-2"><span className="truncate text-sm font-medium text-foreground">{playbook.title}</span>{playbookBrowseCategory(playbook) !== playbook.category && <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">{playbook.category}</span>}<span className="shrink-0 text-[10px] text-muted-foreground">v{playbook.version}</span></div>
                                <p className="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">{playbook.description}</p>
                                <span className="mt-2 inline-flex rounded-full border border-border bg-muted/30 px-2 py-0.5 text-[10px] font-medium text-muted-foreground">{playbook.agentSlots?.length ? 'Crew automation proposal' : 'Workflow guide'}</span>
                                <div className="mt-2 flex flex-wrap items-center gap-2">
                                  <AskAIButton workspacePath={workspacePath} label="Ask about this playbook" message={`Explain the ${JSON.stringify(playbook.title)} playbook (catalog id ${JSON.stringify(playbook.id)}, v${playbook.version}): what it sets up, what inputs it needs, and whether it fits this workflow. If I confirm I want it, walk me through installing it from its detail view and the setup that follows.`} />
                                  <Button type="button" variant="ghost" size="sm" aria-label={`Open ${playbook.title} details`} onClick={() => setSelected(playbook)}>
                                    Details <ChevronRight className="h-3.5 w-3.5" />
                                  </Button>
                                </div>
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </section>
                  )
                })}
              </div>
            )}
          </div>
        </>
      )}
      {!workspacePath && <p className="mt-3 text-xs text-destructive">Open a workflow folder to use playbooks.</p>}
    </div>
  )
}

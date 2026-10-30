import { WorkspacePanelActions } from '../workspace/WorkspacePanelHeaderSlots'
import { useEffect, useCallback, useRef, useState } from 'react'
import { Loader2, AlertCircle, RefreshCw } from 'lucide-react'
import { WorkspacePanelBack } from '../workspace/WorkspacePanelNavigation'
import { skillsApi } from '../../api/skills'
import type { Skill } from '../../types/skills'
import SkillRow from './SkillRow'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { defaultSkillLabel, profileSkillPresentation } from './skillPresentation'

interface SkillsManagerPanelProps {
  compact?: boolean
  selectedSkills?: string[]
  onToggleSkill?: (folderName: string) => void
  workspacePath?: string | null
  emptySelectionText?: string
  selectionScopeLabel?: string
  manageOwnScroll?: boolean
  onAddViaChat?: (skill: Skill) => void
  onBeforeUninstall?: () => Promise<void>
  onUninstalled?: (folderName: string) => Promise<void> | void
  headerAction?: React.ReactNode
  /** Crew presents one inventory, including legacy project skill files. */
  additionalSkills?: Skill[]
  groupBySource?: boolean
  showRefresh?: boolean
  onOpenFile?: (path: string) => void
  profileId?: string
  profileVersion?: number
  /** Product panels show user-installed files; platform guidance stays in the runtime. */
  customOnly?: boolean
  /** Allow owners to manage installed project files in the compact inventory. */
  allowManage?: boolean
}

// All products use the same workspace inventory. Main chat selection and step
// attachments remain independent; this list reports both, plus local CLI skills.
export default function SkillsManagerPanel({
  selectedSkills = [], onToggleSkill, workspacePath,
  emptySelectionText = 'No skills installed here. Ask the agent to add or create one.',
  selectionScopeLabel = 'main chat', manageOwnScroll = true,
  onAddViaChat, onBeforeUninstall, onUninstalled, headerAction,
  additionalSkills = [], groupBySource = true, showRefresh = false, onOpenFile,
  profileId, profileVersion, customOnly = false, allowManage = false,
}: SkillsManagerPanelProps) {
  const [skills, setSkills] = useState<Skill[]>([])
  const [usage, setUsage] = useState<Record<string, string[]>>({})
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [uninstalling, setUninstalling] = useState(false)
  const [defaults, setDefaults] = useState<ReturnType<typeof profileSkillPresentation> | null>(null)
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([server, account, workspacePath, profileId, profileVersion])
  const [loadedScope, setLoadedScope] = useState('')
  const [viewedSkill, setViewedSkill] = useState<string | null>(null)
  const readerView = useRef<HTMLElement>(null)
  const readOnly = !useCanWriteWorkflow(workspacePath)
  const generation = useRef(0)
  const activeScope = useRef(scope)
  activeScope.current = scope

  useEffect(() => {
    if (!viewedSkill) return
    readerView.current?.focus()
    readerView.current?.scrollIntoView?.({ block: 'start' })
  }, [viewedSkill])

  const loadSkills = useCallback(async () => {
    const version = ++generation.current
    setIsLoading(true)
    setError(null)
    try {
      const [inventoryResult, profileResult] = await Promise.allSettled([
        skillsApi.listSkills(workspacePath),
        profileId ? agentApi.getAgentProfile(profileId, profileVersion, true) : Promise.resolve({}),
      ])
      if (version !== generation.current || activeScope.current !== scope) return
      if (inventoryResult.status === 'rejected') throw inventoryResult.reason
      const response = inventoryResult.value
      setSkills(response.skills || [])
      setUsage(response.usage || {})
      setLoadedScope(scope)
      setDefaults(null)
      if (profileResult.status === 'rejected') throw new Error('Product defaults could not be loaded. Installed files are shown; try again to check their inclusion.')
      setDefaults(profileSkillPresentation(profileResult.value))
    } catch (cause) {
      if (version !== generation.current || activeScope.current !== scope) return
      setError(cause instanceof Error ? cause.message : 'Failed to load skills')
    } finally {
      if (version === generation.current && activeScope.current === scope) setIsLoading(false)
    }
  }, [workspacePath, profileId, profileVersion, scope])

  useEffect(() => {
    setSkills([])
    setUsage({})
    setDefaults(null)
    setLoadedScope('')
    setViewedSkill(null)
    void loadSkills()
    const pendingRequests = generation
    const onFocus = () => { void loadSkills() }
    window.addEventListener('focus', onFocus)
    return () => { pendingRequests.current++; window.removeEventListener('focus', onFocus) }
  }, [loadSkills])

  const uninstall = async (name: string) => {
    if (readOnly || !defaults || !workspacePath || uninstalling || loadedScope !== activeScope.current) return
    const labels = usage[name] || []
    const dependencies = labels.length ? ` It is used by ${labels.join(', ')}.` : ''
    if (!window.confirm(`Uninstall "${name}" from this workspace?${dependencies} Its files and attachments in this workspace will be removed.`)) return
    setUninstalling(true)
    try {
      await onBeforeUninstall?.()
      if (activeScope.current !== scope) return
      await skillsApi.deleteSkill(name, workspacePath)
      if (activeScope.current !== scope) return
      await onUninstalled?.(name)
      await loadSkills()
    } catch (cause) {
      if (activeScope.current === scope) setError(cause instanceof Error ? cause.message : 'Failed to uninstall skill')
    } finally { setUninstalling(false) }
  }

  const allSkills = loadedScope === scope ? [...skills, ...additionalSkills.filter(extra => !skills.some(skill => skill.folder_name === extra.folder_name))] : []
  const defaultModes = new Map<string, string[]>()
  for (const [mode, names] of Object.entries(defaults?.byMode || {})) {
    for (const name of names) defaultModes.set(name, [...(defaultModes.get(name) || []), mode])
  }
  const completeInventory: Skill[] = [...allSkills, ...[...defaultModes.keys()]
    .filter(name => !allSkills.some(skill => skill.folder_name === name))
    .map(name => Object.values(defaults?.definitionsByMode || {}).flat().find(skill => skill.folder_name === name) || { folder_name: name, file_path: '', frontmatter: { name, description: '' }, content: '' })]
  const isPlatform = (skill: Skill) => skill.managed || skill.file_managed || defaultModes.has(skill.folder_name)
  const isCustom = (skill: Skill) => {
    if (isPlatform(skill) || !skill.file_path || skill.file_path === 'Platform') return false
    if (skill.source || skill.source_url || skill.brain_source) return true
    const relativePath = workspacePath && skill.file_path.startsWith(`${workspacePath}/`)
      ? skill.file_path.slice(workspacePath.length + 1) : skill.file_path
    // Explicit project skill files are user-owned. An old provider projection
    // without installation provenance must not be presented as user-installed.
    return relativePath.startsWith('skills/') || relativePath.startsWith('.skills/')
  }
  const inventory = customOnly ? completeInventory.filter(isCustom) : completeInventory
  const missing = [...new Set([...selectedSkills, ...Object.keys(usage)])]
    .filter(name => !completeInventory.some(skill => skill.folder_name === name))
  const groups = customOnly ? [
    { title: 'Custom skills', description: `Extra skill files in this ${selectionScopeLabel}, added by a person, a chat or an import. This list does not identify who installed them. ${allowManage ? 'Open a skill to edit or remove it, or ask in chat.' : 'Ask in chat to inspect, add or remove one.'}`, skills: inventory },
  ] : groupBySource ? [
    { title: 'Project skills', description: 'Skill files in this project. Their selection and usage are shown below.', skills: inventory.filter(skill => !isPlatform(skill)) },
    { title: 'Platform skills', description: 'Platform-managed files and declared defaults for Builder and Run. Actual access is checked when a session runs.', skills: inventory.filter(isPlatform) },
  ] : [{ title: '', description: '', skills: inventory }]
  const row = (skill: Skill, reader = false) => {
    const installed = skills.some(item => item.folder_name === skill.folder_name)
    const included = defaultModes.has(skill.folder_name)
    const selectable = !customOnly && installed && !included
    return <SkillRow key={skill.folder_name} skill={{ ...skill, used_by: [...new Set([...(usage[skill.folder_name] || skill.used_by || []), ...(selectedSkills.includes(skill.folder_name) ? ['Main chat'] : [])])] }}
      onRead={() => {
        if (!skill.content && skill.file_path && skill.file_path !== 'Platform' && onOpenFile) onOpenFile(skill.file_path)
        else setViewedSkill(skill.folder_name)
      }} reader={reader}
      defaultDefinitions={Object.entries(defaults?.definitionsByMode || {}).flatMap(([mode, definitions]) => definitions.filter(definition => definition.folder_name === skill.folder_name).map(definition => ({ mode, skill: definition })))}
      onDelete={(!customOnly || allowManage) && installed && !skill.managed && !skill.file_managed && !skill.builtin && !included ? () => { void uninstall(skill.folder_name) } : undefined}
      inventoryOnly={customOnly}
      selected={selectable && onToggleSkill ? selectedSkills.includes(skill.folder_name) : undefined}
      onToggleSelect={selectable && onToggleSkill ? () => { if (!readOnly && defaults && loadedScope === activeScope.current) onToggleSkill(skill.folder_name) } : undefined}
      onRequestAdd={selectable && onToggleSkill && onAddViaChat ? () => { if (!readOnly && defaults && loadedScope === activeScope.current) onAddViaChat(skill) } : undefined}
      inclusionLabels={(defaultModes.get(skill.folder_name) || []).map(defaultSkillLabel)}
      platformStatus={skill.builtin ? (skill.file_path === 'Platform' ? 'Provided by platform' : 'Platform version runs') : skill.file_managed ? 'Platform-managed file' : skill.managed ? 'Managed by platform' : undefined}
      onOpenFile={onOpenFile && skill.file_path && skill.file_path !== 'Platform' ? () => onOpenFile(skill.file_path) : undefined}
      readable={groupBySource}
      readOnly={readOnly || uninstalling || !defaults} selectionScopeLabel={selectionScopeLabel} />
  }
  const reading = inventory.find(skill => skill.folder_name === viewedSkill)
  return (
    <div className={`${manageOwnScroll ? 'flex min-h-0 flex-1 flex-col' : 'flex flex-col'} gap-2`}>
      {(headerAction || showRefresh) && <WorkspacePanelActions>
        {headerAction}
        {showRefresh && <button type="button" onClick={() => { void loadSkills() }} disabled={uninstalling || isLoading}
          title="Refresh skills" aria-label="Refresh skills"
          className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground hover:text-primary">
          <RefreshCw className={`h-3.5 w-3.5 ${isLoading ? 'animate-spin' : ''}`} />
        </button>}
      </WorkspacePanelActions>}
      {error && <div role="alert" className="flex items-center gap-2 text-sm text-destructive"><AlertCircle className="h-4 w-4 shrink-0" /><span>{error}</span><button type="button" disabled={isLoading || uninstalling} onClick={() => { void loadSkills() }} className="ml-auto shrink-0 rounded px-2 py-1 text-xs underline disabled:opacity-50">Try again</button></div>}
      <div className={manageOwnScroll ? 'min-h-0 flex-1 overflow-y-auto' : ''}>
        {(isLoading && loadedScope !== scope) ? <div className="flex gap-2 py-4 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading skills...</div>
          : loadedScope === scope && <div className="space-y-6">
            {reading ? <section ref={readerView} tabIndex={-1} aria-label={`Skill instructions: ${reading.frontmatter.name}`} className="outline-none">
              <WorkspacePanelBack label="Back to Skills" onBack={() => setViewedSkill(null)} />
              {row(reading, true)}
            </section> : <>
            {!customOnly && profileId && defaults && !defaults.modesReported && <p className="text-xs text-muted-foreground">This server reports profile defaults only. Builder and Run inclusion is unavailable.</p>}
            {groups.map(group => <section key={group.title || 'skills'} aria-label={group.title || 'Installed skills'}>
              {group.title && <div className="mb-3"><h3 className="text-sm font-semibold">{group.title} <span className="ml-1 text-xs font-normal text-muted-foreground">{group.skills.length}</span></h3><p className="mt-1 text-xs leading-5 text-muted-foreground">{group.description}</p></div>}
              {group.skills.length ? <div className="overflow-hidden rounded-lg border border-border">{[...group.skills].sort((a, b) => a.frontmatter.name.localeCompare(b.frontmatter.name)).map(skill => row(skill))}</div> : <p className="rounded-lg border border-dashed border-border px-3 py-4 text-xs text-muted-foreground">{group.title === 'Platform skills' ? 'No platform skills available.' : emptySelectionText}</p>}
            </section>)}
            {!customOnly && !!missing.length && <section aria-label="Unavailable skills" className="rounded-lg border border-border">{missing.map(name => <div key={name} className="border-b border-border px-3 py-2 text-sm last:border-b-0">
              <div className="flex items-center justify-between gap-2"><span>{name}</span>
                <button type="button" disabled={readOnly || uninstalling || !defaults} onClick={() => { void uninstall(name) }} className="text-xs text-primary disabled:opacity-50">Remove attachment</button>
              </div>
              <p className="text-xs text-muted-foreground">{usage[name]?.join(' · ') || 'Main chat'} · Files unavailable</p>
            </div>)}</section>}
            </>}
          </div>}
      </div>
    </div>
  )
}

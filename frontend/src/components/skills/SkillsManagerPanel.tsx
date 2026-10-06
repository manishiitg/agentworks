import { useEffect, useCallback, useRef, useState } from 'react'
import { Loader2, AlertCircle, RefreshCw } from 'lucide-react'
import { skillsApi } from '../../api/skills'
import type { Skill } from '../../types/skills'
import SkillRow from './SkillRow'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'

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
}

// All products use the same workspace inventory. Main chat selection and step
// attachments remain independent; this list reports both, plus local CLI skills.
export default function SkillsManagerPanel({
  selectedSkills = [], onToggleSkill, workspacePath,
  emptySelectionText = 'No skills installed here. Ask the agent to add or create one.',
  selectionScopeLabel = 'main chat', manageOwnScroll = true,
  onAddViaChat, onBeforeUninstall, onUninstalled, headerAction,
}: SkillsManagerPanelProps) {
  const [skills, setSkills] = useState<Skill[]>([])
  const [usage, setUsage] = useState<Record<string, string[]>>({})
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [uninstalling, setUninstalling] = useState(false)
  const readOnly = !useCanWriteWorkflow(workspacePath)
  const generation = useRef(0)
  const activeWorkspace = useRef(workspacePath)
  activeWorkspace.current = workspacePath

  const loadSkills = useCallback(async () => {
    const version = ++generation.current
    setIsLoading(true)
    setError(null)
    try {
      const response = await skillsApi.listSkills(workspacePath)
      if (version !== generation.current || activeWorkspace.current !== workspacePath) return
      setSkills(response.skills || [])
      setUsage(response.usage || {})
    } catch (cause) {
      if (version !== generation.current || activeWorkspace.current !== workspacePath) return
      setError(cause instanceof Error ? cause.message : 'Failed to load skills')
    } finally {
      if (version === generation.current && activeWorkspace.current === workspacePath) setIsLoading(false)
    }
  }, [workspacePath])

  useEffect(() => {
    setSkills([])
    setUsage({})
    void loadSkills()
    const pendingRequests = generation
    const onFocus = () => { void loadSkills() }
    window.addEventListener('focus', onFocus)
    return () => { pendingRequests.current++; window.removeEventListener('focus', onFocus) }
  }, [loadSkills])

  const uninstall = async (name: string) => {
    if (readOnly || !workspacePath || uninstalling) return
    const labels = usage[name] || []
    const dependencies = labels.length ? ` It is used by ${labels.join(', ')}.` : ''
    if (!window.confirm(`Uninstall "${name}" from this workspace?${dependencies} Its files and attachments in this workspace will be removed.`)) return
    setUninstalling(true)
    try {
      await onBeforeUninstall?.()
      if (activeWorkspace.current !== workspacePath) return
      await skillsApi.deleteSkill(name, workspacePath)
      if (activeWorkspace.current !== workspacePath) return
      await onUninstalled?.(name)
      await loadSkills()
    } catch (cause) {
      if (activeWorkspace.current === workspacePath) setError(cause instanceof Error ? cause.message : 'Failed to uninstall skill')
    } finally { setUninstalling(false) }
  }

  const missing = [...new Set([...selectedSkills, ...Object.keys(usage)])]
    .filter(name => !skills.some(skill => skill.folder_name === name))
  return (
    <div className={`${manageOwnScroll ? 'flex min-h-0 flex-1 flex-col' : 'flex flex-col'} gap-2`}>
      <div className="flex justify-end gap-2">
        {headerAction}
        <button type="button" onClick={() => { void loadSkills() }} disabled={uninstalling}
          title="Refresh skills" aria-label="Refresh skills"
          className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground hover:text-primary">
          <RefreshCw className={`h-3.5 w-3.5 ${isLoading ? 'animate-spin' : ''}`} />
        </button>
      </div>
      {error && <div role="alert" className="flex items-center gap-2 text-sm text-red-500"><AlertCircle className="h-4 w-4" />{error}</div>}
      <div className={manageOwnScroll ? 'min-h-0 flex-1 overflow-y-auto' : ''}>
        {isLoading && !skills.length ? <div className="flex gap-2 py-4 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading skills...</div>
          : !skills.length && !missing.length ? <p className="py-6 text-center text-sm text-muted-foreground">{emptySelectionText}</p>
          : <div className="rounded-md border border-border">
            {skills.map(skill => <SkillRow key={skill.folder_name} skill={{ ...skill, used_by: [...new Set([...(usage[skill.folder_name] || skill.used_by || []), ...(selectedSkills.includes(skill.folder_name) ? ['Main chat'] : [])])] }}
              onDelete={skill.managed ? undefined : () => { void uninstall(skill.folder_name) }}
              selected={onToggleSkill ? selectedSkills.includes(skill.folder_name) : undefined}
              onToggleSelect={onToggleSkill ? () => onToggleSkill(skill.folder_name) : undefined}
              onRequestAdd={onToggleSkill && onAddViaChat ? () => onAddViaChat(skill) : undefined}
              readOnly={readOnly || uninstalling} selectionScopeLabel={selectionScopeLabel} />)}
            {missing.map(name => <div key={name} className="border-b border-border px-3 py-2 text-sm last:border-b-0">
              <div className="flex items-center justify-between gap-2"><span>{name}</span>
                <button type="button" disabled={readOnly || uninstalling} onClick={() => { void uninstall(name) }} className="text-xs text-primary disabled:opacity-50">Remove attachment</button>
              </div>
              <p className="text-xs text-muted-foreground">{usage[name]?.join(' · ') || 'Main chat'} · Files unavailable</p>
            </div>)}
          </div>}
      </div>
    </div>
  )
}

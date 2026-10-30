import { useCallback, useEffect, useState } from 'react'
import { BookOpen } from 'lucide-react'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import type { PlanningResponse } from '../../utils/stepConfigMatching'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import KnowledgebaseView from './KnowledgebaseView'
import DatabaseView from './DatabaseView'

type KnowledgeTabId = 'knowledgebase' | 'database'

const KNOWLEDGE_TABS: Array<{ value: KnowledgeTabId; label: string }> = [
  { value: 'knowledgebase', label: 'Knowledgebase' },
  { value: 'database', label: 'Database' },
]

function isKnowledgeTab(value: unknown): value is KnowledgeTabId {
  return value === 'knowledgebase' || value === 'database'
}

interface KnowledgeViewProps {
  workspacePath: string | null
  plan: PlanningResponse | null
}

/**
 * The Knowledge umbrella: background notes and stored records under one
 * header with tabs. Tab switches write back through openWorkspaceView (hub
 * pattern) so agent deep-links, refresh, and remounts land on the same tab.
 */
export default function KnowledgeView({ workspacePath }: KnowledgeViewProps) {
  const workspaceViewTarget = useWorkflowStore(state => state.workspaceViewTarget)
  const initialTab = workspaceViewTarget?.view === 'knowledge' && isKnowledgeTab(workspaceViewTarget.target)
    ? workspaceViewTarget.target
    : 'knowledgebase'
  const [tab, setTab] = useState<KnowledgeTabId>(initialTab)

  useEffect(() => {
    if (workspaceViewTarget?.view !== 'knowledge') return
    if (isKnowledgeTab(workspaceViewTarget.target)) setTab(workspaceViewTarget.target)
  }, [workspaceViewTarget])

  const selectTab = useCallback((next: KnowledgeTabId) => {
    setTab(next)
    useWorkflowStore.getState().openWorkspaceView('knowledge', next)
  }, [])

  const tabLabel = KNOWLEDGE_TABS.find(option => option.value === tab)?.label ?? 'Knowledge'

  return (
    <div className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
      <WorkspaceViewHeader
        icon={BookOpen}
        title="Knowledge"
        actions={(
          <WorkspaceViewActions
            onRefresh={() => useWorkflowStore.getState().refreshWorkspaceView(tab)}
            refreshLabel={`Refresh ${tabLabel}`}
          />
        )}
        /* Knowledge base and Database are separate side-list items (owner, 2026-10-20). */
      />
      <div className="min-h-0 flex-1">
        {tab === 'knowledgebase' && <KnowledgebaseView workspacePath={workspacePath} hideHeader />}
        {tab === 'database' && <DatabaseView workspacePath={workspacePath} hideHeader />}
      </div>
    </div>
  )
}

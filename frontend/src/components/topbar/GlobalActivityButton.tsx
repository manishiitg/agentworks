import { useCallback, useEffect, useRef, useState } from 'react'
import { Inbox } from 'lucide-react'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

/** A decision signal in the existing Goals header, never a global sidebar item. */
export function GlobalActivityButton({ workspacePaths, active, onOpen }: { workspacePaths: string[]; active: boolean; onOpen: () => void }) {
  const user = useAuthStore(state => state.user?.id ?? '')
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId ?? '')
  const key = JSON.stringify([user, server, [...new Set(workspacePaths)].sort()])
  const [result, setResult] = useState<{ key: string; count: number } | null>(null)
  const generation = useRef(0)
  const refresh = useCallback(async () => {
    const current = ++generation.current
    const [, , paths] = JSON.parse(key) as [string, string, string[]]
    if (!paths.length) { setResult({ key, count: 0 }); return }
    try {
      const response = await agentApi.listNeedsYouDecisionsAggregate(paths)
      if (current === generation.current) setResult({ key, count: response.success ? response.inputs.filter(input => paths.includes(input.workspace_path)).length : 0 })
    } catch { if (current === generation.current) setResult({ key, count: 0 }) }
  }, [key])
  useEffect(() => {
    if (!document.hidden) void refresh()
    return () => { generation.current++ }
  }, [refresh])
  useLiveRefetch(() => { void refresh() }, { kinds: ['human_inputs'], fallbackMs: 30_000 })
  const count = result?.key === key ? result.count : 0
  if (!count) return null
  return <Tooltip><TooltipTrigger asChild>
    <button type="button" onClick={onOpen} aria-label={`Needs you: ${count} ${count === 1 ? 'decision' : 'decisions'}`} aria-pressed={active}
      className="inline-flex h-7 items-center gap-1 rounded-md px-1.5 text-amber-600 hover:bg-amber-500/10 dark:text-amber-400">
      <Inbox aria-hidden="true" className="h-4 w-4" /><span className="text-[11px] font-medium">{count}</span>
    </button>
  </TooltipTrigger><TooltipContent>Needs you · Review Goal decisions</TooltipContent></Tooltip>
}

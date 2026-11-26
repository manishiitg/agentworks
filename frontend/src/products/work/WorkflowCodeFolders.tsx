import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, FileCode2, Folder, Workflow } from 'lucide-react'
import api from '../../services/api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import type { CrewExecutable } from './CrewExecutableEditor'

/** The code files a workflow's page lists, in the same order: main.py and script.json, then its other files. */
export const workflowCodeFiles = (item: CrewExecutable) => ['main.py', 'script.json', ...(item.files ?? [])]

/**
 * The Files panel's Workflows folder (#4151): one folder per workflow of this project, named by its label, holding
 * only its code. A file opens in the same Files viewer as every other project file; the server saves it through the
 * workflow's own checks. Runs, the database and settings stay out. Nothing shows when the project has no workflow
 * or the list is not the caller's to read (only the project's Builder lists its workflows).
 */
export function WorkflowCodeFolders({ projectId, onOpen }: { projectId: string; onOpen: (path: string) => void }) {
  const [items, setItems] = useState<CrewExecutable[]>([])
  const [open, setOpen] = useState(true)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const selected = useWorkspaceStore(state => state.selectedFile?.path ?? '')
  const load = () => api.get<{ executables: CrewExecutable[] }>('/api/crew-executables', { params: { crew_id: projectId } })
    .then(({ data }) => setItems((data.executables ?? []).filter(item => item.kind === 'script')))
    .catch(() => setItems([]))
  useEffect(() => { setItems([]); void load() }, [projectId]) // eslint-disable-line react-hooks/exhaustive-deps
  useLiveRefetch(() => { void load() }, { kinds: ['plan'], refreshOnFocus: true, fallbackMs: 60_000, safetyMs: 120_000, minIntervalMs: 5_000 })
  if (!items.length) return null
  const toggle = (id: string) => setExpanded(current => { const next = new Set(current); if (!next.delete(id)) next.add(id); return next })
  const row = 'flex w-full min-w-0 items-center gap-1.5 rounded px-2 py-1 text-left text-[13px] hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <section aria-label="Workflows" data-testid="workflow-code-folders" className="border-t border-border py-1">
    <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className={`${row} font-medium`}>
      {open ? <ChevronDown className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
      <Workflow className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />Workflows
    </button>
    {open && items.map(item => {
      const shown = expanded.has(item.id)
      return <div key={item.id}>
        <button type="button" aria-expanded={shown} onClick={() => toggle(item.id)} className={`${row} pl-6`} title={item.manifest.description || item.manifest.label}>
          {shown ? <ChevronDown className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
          <Folder className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span className="truncate">{item.manifest.label}</span>
        </button>
        {shown && workflowCodeFiles(item).map(file => {
          const filePath = `${item.workspace_path}/code/${file}`
          return <button key={file} type="button" onClick={() => onOpen(filePath)} aria-current={selected === filePath ? 'true' : undefined}
            className={`${row} pl-12 ${selected === filePath ? 'bg-muted font-medium' : ''}`}>
            <FileCode2 className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span className="truncate">{file}</span>
          </button>
        })}
      </div>
    })}
  </section>
}

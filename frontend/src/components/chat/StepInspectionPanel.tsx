import { lazy, Suspense, useEffect, useId, useMemo, useRef, useState } from 'react'
import { AlertCircle, RefreshCw } from 'lucide-react'
import type { ExecutionInspectionTarget, ExecutionInspectionResponse } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { inspectionRecords, inspectionTaskLabel } from '../../utils/inspectionConversation'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

const ExecutionLogsPopup = lazy(() => import('../workflow/ExecutionLogsPopup'))
type Section = 'system' | 'user' | 'tools' | 'result'
const sectionLabels: Record<Section, string> = {system: 'System prompt', user: 'User message', tools: 'Tools', result: 'Result'}

function useExecutionRecords(target: ExecutionInspectionTarget) {
 const [data, setData] = useState<ExecutionInspectionResponse | null>(null)
 const [error, setError] = useState('')
 const [refresh, setRefresh] = useState(0)
 useEffect(() => {
  let disposed = false
  let timer: ReturnType<typeof setTimeout> | undefined
  let terminalReads = 0
  let missingReads = 0
  const identity = captureChatIdentity()
  setData(null); setError('')
  const load = async () => {
   try {
    const response = await agentApi.inspectSessionExecution(target)
    if (disposed || !isChatIdentityCurrent(identity)) return
    if (response.transcript.session_id !== target.session_id || response.transcript.agent_id !== target.execution_id) throw new Error('Execution identity did not match')
    missingReads = 0
    setData(response); setError('')
    terminalReads = response.transcript.status === 'running' ? 0 : terminalReads + 1
   } catch (failure) {
    if (disposed || !isChatIdentityCurrent(identity)) return
    const status = (failure as {response?: {status?: number}}).response?.status
    if (status === 404) setData(null)
    if (status === 404 && ++missingReads >= 3) {setError('No saved records are available for this execution. Try Refresh or Saved logs.'); return}
    if (status === 401 || status === 403) {setData(null); setError('Access to this execution is no longer available'); return}
    setError('Execution records are temporarily unavailable. Retrying…')
   }
   if (!disposed && isChatIdentityCurrent(identity) && terminalReads < 3) timer = setTimeout(() => {void load()}, 2500)
  }
  void load()
  return () => {disposed = true; clearTimeout(timer)}
 }, [target, refresh])
 // Never show a prior execution while an identity change starts its request.
 const current = data?.transcript.agent_id === target.execution_id && data.transcript.session_id === target.session_id ? data : null
 return {data: current, error, refresh: () => setRefresh(value => value + 1)}
}

function ExecutionRecords({data, initialSection = 'user'}: {data: ExecutionInspectionResponse; initialSection?: Section}) {
 const [section, setSection] = useState<Section>(initialSection)
 const id = useId()
 const tabs = useRef<HTMLDivElement>(null)
 const records = useMemo(() => inspectionRecords(data), [data])
 const missing = (label: string) => <p className="text-xs text-muted-foreground">{data.transcript.status === 'running' ? `${label} will appear when recorded.` : `No ${label.toLowerCase()} was captured for this execution. Check Saved logs below for any retained evidence.`}</p>
 return <>
  {data.transcript.error && <p className="mb-3 whitespace-pre-wrap text-xs text-destructive">{data.transcript.error}</p>}
  <div ref={tabs} className="mb-3 flex flex-wrap gap-1 border-b border-border pb-2" role="tablist" aria-label="Execution records" onKeyDown={event => {
   if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) || (event.target as HTMLElement).getAttribute('role') !== 'tab') return
   const sections = Object.keys(sectionLabels) as Section[]
   const next = event.key === 'Home' ? 0 : event.key === 'End' ? sections.length - 1 : (sections.indexOf(section) + (event.key === 'ArrowRight' ? 1 : -1) + sections.length) % sections.length
   event.preventDefault(); setSection(sections[next]); tabs.current?.querySelectorAll<HTMLButtonElement>('button')[next]?.focus()
  }}>{(Object.keys(sectionLabels) as Section[]).map(value => <button type="button" key={value} role="tab" id={`${id}-${value}`} aria-controls={`${id}-records`} tabIndex={section === value ? 0 : -1} aria-selected={section === value} onClick={() => setSection(value)} className={`rounded px-2 py-1 text-xs ${section === value ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{sectionLabels[value]}{value === 'tools' && records.tools.length > 0 ? ` (${records.tools.length})` : ''}</button>)}</div>
  <div role="tabpanel" id={`${id}-records`} aria-labelledby={`${id}-${section}`}>
   {section === 'system' && (records.system.length ? records.system.map((text, index) => <pre key={index} className="mb-3 whitespace-pre-wrap break-words rounded border border-border p-3 text-xs">{text}</pre>) : missing('System prompt'))}
   {section === 'user' && (records.user.length ? records.user.map((text, index) => <div key={index} className="mb-3 rounded border border-border p-3"><p className="mb-2 text-[11px] text-muted-foreground">{records.user.length > 1 ? `User message ${index + 1}` : 'User message sent to this task'}</p><pre className="whitespace-pre-wrap break-words text-xs">{text}</pre></div>) : missing('User message'))}
   {section === 'tools' && (records.tools.length ? <div className="space-y-2">{records.tools.map((tool, index) => <details key={tool.tool_call_id || index} className="rounded border border-border p-3"><summary className="cursor-pointer text-xs"><span className="font-medium">{tool.tool_name}</span><span className="ml-2 text-muted-foreground">{tool.status}</span></summary><p className="mt-3 text-[11px] font-medium">Inputs</p><pre className="mt-1 whitespace-pre-wrap break-words text-xs">{tool.arguments || 'No inputs recorded.'}</pre><p className="mt-3 text-[11px] font-medium">{tool.error ? 'Error' : 'Result'}</p><pre className="mt-1 whitespace-pre-wrap break-words text-xs">{tool.error || tool.result || (tool.status === 'running' ? 'Running…' : 'No result recorded.')}</pre></details>)}</div> : missing('Tool calls'))}
   {section === 'result' && (records.result ? <MarkdownRenderer content={records.result} /> : missing('Result'))}
  </div>
 </>
}

function TaskRecords({target}: {target: ExecutionInspectionTarget}) {
 const {data, error, refresh} = useExecutionRecords(target)
 return <><div className="mb-3 flex items-center gap-2"><h3 className="min-w-0 flex-1 text-xs font-medium">{inspectionTaskLabel(target.name)}</h3><span className="text-xs text-muted-foreground">{data?.transcript.status || 'Loading…'}</span><button type="button" aria-label="Refresh task inspection" title="Refresh task" onClick={refresh} className="rounded p-1 text-muted-foreground hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" /></button></div>{error && <p role="status" className="mb-3 text-xs text-amber-600">{error}</p>}{data ? <ExecutionRecords data={data} /> : <p className="text-xs text-muted-foreground">Loading task records…</p>}</>
}

export default function StepInspectionPanel({target}: {target: ExecutionInspectionTarget}) {
 const {data, error, refresh} = useExecutionRecords(target)
 const [chosen, setChosen] = useState<string | null>(null)
 const [logsOpen, setLogsOpen] = useState(false)
 const transcript = data?.transcript
 const actual = transcript?.inspection ?? target
 const children = data?.children ?? []
 // Delegating parents have no provider conversation. Open an actionable task
 // immediately, while allowing explicit selection of the step's own result.
 const preferred = children.find(child => child.status === 'failed') || children.find(child => child.status === 'running') || children.find(child => !/\(prevalidation\)$/.test(child.name)) || children[0]
 const selectedID = chosen ?? (!transcript?.events.length ? preferred?.agent_id : undefined) ?? target.execution_id
 const child = children.find(item => item.agent_id === selectedID)
 const childTarget = useMemo(() => child ? {workspace_path: target.workspace_path, session_id: target.session_id, execution_id: child.agent_id, name: child.name, started_at: child.started_at || target.started_at} : null, [target, child?.agent_id, child?.name, child?.started_at])
 const labelsId = useId()
 return <div className="flex h-full min-h-0 flex-col">
  <div className="shrink-0 border-b border-border px-3 py-2">
   <div className="flex items-center gap-2"><h2 className="min-w-0 flex-1 truncate text-sm font-medium" title={transcript?.name || target.name}>{transcript?.name || target.name || 'Step'}</h2><span className="text-xs text-muted-foreground">{transcript?.status || 'Loading…'}</span><button type="button" aria-label="Refresh step inspection" title="Refresh step" onClick={refresh} className="rounded p-1 text-muted-foreground hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" /></button></div>
   {children.length > 0 && <div className="mt-3 flex items-center gap-2"><label htmlFor={`${labelsId}-task`} className="shrink-0 text-xs text-muted-foreground">Inspect task</label><select id={`${labelsId}-task`} value={selectedID} onChange={event => setChosen(event.target.value)} className="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1.5 text-xs"><option value={target.execution_id}>Step result · {transcript?.status}</option>{children.map(item => <option key={item.agent_id} value={item.agent_id}>{inspectionTaskLabel(item.name)} · {item.status}</option>)}</select></div>}
  </div>
  <div className="min-h-0 flex-1 overflow-y-auto p-3">
   {error && <div role="status" className="mb-3 flex gap-2 text-xs text-amber-600"><AlertCircle className="h-4 w-4 shrink-0" />{error}</div>}
   {childTarget ? <TaskRecords key={childTarget.execution_id} target={childTarget} /> : data ? <ExecutionRecords data={data} initialSection={children.length ? 'result' : 'user'} /> : <p className="text-xs text-muted-foreground">Loading execution records…</p>}
   <details className="mt-5 border-t border-border pt-3 text-xs"><summary className="cursor-pointer text-muted-foreground">Technical details and saved logs</summary><dl className="my-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]"><dt>Execution</dt><dd className="break-all">{selectedID}</dd>{actual.step_id && <><dt>Step</dt><dd>{actual.step_id}</dd></>}{actual.run_folder && <><dt>Run folder</dt><dd>{actual.run_folder}</dd></>}<dt>Started</dt><dd>{new Date(childTarget?.started_at || transcript?.started_at || target.started_at).toLocaleString()}</dd></dl>
    {actual.run_folder && actual.step_id && <><button type="button" onClick={() => setLogsOpen(value => !value)} className="rounded border border-border px-2 py-1.5">{logsOpen ? 'Hide saved logs' : 'Open saved logs'}</button>{logsOpen && <div className="mt-3 h-96 min-h-0"><p className="mb-2 text-[11px] text-muted-foreground">Saved attempts for this step. These may include other executions; check their timestamps.</p><Suspense fallback={<p>Loading saved logs…</p>}><ExecutionLogsPopup workspacePath={target.workspace_path} runFolder={actual.run_folder} runFolders={[actual.run_folder]} initialStepId={actual.step_id} allowDefaultRunFolder={false} /></Suspense></div>}</>}
   </details>
  </div>
 </div>
}

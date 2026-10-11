import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { AlertCircle, RefreshCw } from 'lucide-react'
import type { ExecutionInspectionTarget, ExecutionInspectionResponse } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { openStepInspection } from '../../utils/stepInspection'
import { inspectionConversation } from '../../utils/inspectionConversation'
import { ConversationViewer } from '../workflow/ConversationViewer'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

const ExecutionLogsPopup = lazy(() => import('../workflow/ExecutionLogsPopup'))

export default function StepInspectionPanel({target}: {target: ExecutionInspectionTarget}) {
 const [data, setData] = useState<ExecutionInspectionResponse | null>(null)
 const [error, setError] = useState('')
 const [updatedAt, setUpdatedAt] = useState('')
 const [section, setSection] = useState<'conversation' | 'tools' | 'outputs' | 'logs'>('conversation')
 const [refresh, setRefresh] = useState(0)
 useEffect(() => {
  let disposed = false
  let timer: ReturnType<typeof setTimeout> | undefined
  let terminalReads = 0
  let missingReads = 0
  const identity = captureChatIdentity()
  const load = async () => {
   try {
    const response = await agentApi.inspectSessionExecution(target)
    if (disposed || !isChatIdentityCurrent(identity)) return
    if (response.transcript.session_id !== target.session_id || response.transcript.agent_id !== target.execution_id) throw new Error('Execution identity did not match')
    missingReads = 0
    setData(response); setError(''); setUpdatedAt(new Date().toLocaleTimeString())
    terminalReads = response.transcript.status === 'running' ? 0 : terminalReads + 1
   } catch (failure) {
    if (disposed || !isChatIdentityCurrent(identity)) return
    const status = (failure as {response?: {status?: number}}).response?.status
    if (status === 404) setData(null)
    if (status === 404 && ++missingReads >= 3) {setData(null); setError('No saved records are available for this execution. Use Refresh to check again.'); return}
    if (status === 401 || status === 403) {setData(null); setError('Access to this execution is no longer available'); return}
    setError('Execution records are temporarily unavailable. Retrying…')
   }
   // Give asynchronous evidence writers two final reads after completion.
   if (!disposed && isChatIdentityCurrent(identity) && terminalReads < 3) timer = setTimeout(() => {void load()}, 2500)
  }
  void load()
  return () => {disposed = true; clearTimeout(timer)}
 }, [target, refresh])
 const content = useMemo(() => data ? inspectionConversation(data, section === 'tools') : '', [data, section])
 const transcript = data?.transcript
 const actual = transcript?.inspection ?? target
 return <div className="flex h-full min-h-0 flex-col">
  <div className="shrink-0 border-b border-border px-3 py-2">
   <div className="flex items-center gap-2"><span className="min-w-0 flex-1 truncate text-sm font-medium">{transcript?.name || target.name || 'Step'}</span><span className="text-xs text-muted-foreground">{transcript?.status || 'Loading…'}</span>
    <button type="button" aria-label="Refresh step inspection" title="Refresh step inspection" onClick={() => setRefresh(value => value + 1)} className="rounded p-1 text-muted-foreground hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" /></button>
   </div>
   <div className="mt-1 flex flex-wrap gap-x-2 text-[10px] text-muted-foreground">
    {actual.step_id && <span>{actual.step_id}</span>}{actual.run_folder && <span>{actual.run_folder}</span>}
    <span title={target.execution_id}>Started {new Date(transcript?.started_at || target.started_at).toLocaleString()}</span>
    {updatedAt && <span>Updated {updatedAt}</span>}
   </div>
   {data && data.children.length > 0 && <div className="mt-2 flex flex-wrap gap-1">{data.children.map(child => <button type="button" key={child.agent_id} onClick={() => openStepInspection({workspace_path: target.workspace_path, session_id: target.session_id, execution_id: child.agent_id, name: child.name, started_at: child.started_at || target.started_at})} className="rounded border border-border px-2 py-1 text-[11px] hover:bg-muted" title="Inspect this agent execution">{child.name} · {child.status}</button>)}</div>}
  </div>
  <div className="flex shrink-0 gap-1 border-b border-border px-3 py-1" role="tablist" aria-label="Step records">{(['conversation', 'tools', 'outputs', ...(actual.step_id && actual.run_folder ? ['logs' as const] : [])] as const).map(value => <button key={value} type="button" role="tab" aria-selected={section === value} onClick={() => setSection(value)} className={`rounded px-2 py-1 text-xs capitalize ${section === value ? 'bg-muted text-foreground' : 'text-muted-foreground'}`}>{value === 'logs' ? 'Saved logs' : value}</button>)}</div>
  <div className="min-h-0 flex-1 overflow-y-auto p-3" role="tabpanel">
   {error && <div role="status" className="mb-3 flex gap-2 text-xs text-amber-600"><AlertCircle className="h-4 w-4 shrink-0" />{error}</div>}
   {transcript?.error && <div className="mb-3 whitespace-pre-wrap text-xs text-destructive">{transcript.error}</div>}
   {section === 'logs' && actual.run_folder && actual.step_id ? <div className="flex h-full min-h-0 flex-col"><p className="mb-2 shrink-0 text-[11px] text-muted-foreground">All saved attempts for this step in {actual.run_folder}. Expand an attempt to inspect its timestamp, prompts and artifacts.</p><div className="min-h-0 flex-1"><Suspense fallback={<p className="text-xs text-muted-foreground">Loading saved logs…</p>}><ExecutionLogsPopup workspacePath={target.workspace_path} runFolder={actual.run_folder} runFolders={[actual.run_folder]} initialStepId={actual.step_id} allowDefaultRunFolder={false} /></Suspense></div></div> : !data ? <p className="text-xs text-muted-foreground">Waiting for saved execution records…</p> : section === 'outputs' ?
    data.result ? <MarkdownRenderer content={data.result} /> : <p className="text-xs text-muted-foreground">{transcript?.status === 'running' ? 'The result will appear when this execution finishes.' : 'No output was recorded for this execution.'}</p> :
    transcript?.events.length ? <ConversationViewer content={content} /> : <p className="text-xs text-muted-foreground">No messages or tools recorded yet. Inspect an agent execution above when this step delegates its work.</p>}
  </div>
 </div>
}

import { lazy, Suspense, useEffect, useId, useMemo, useState } from 'react'
import { AlertCircle, RefreshCw } from 'lucide-react'
import type { ExecutionInspectionTarget, ExecutionInspectionResponse } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { inspectionChat, inspectionRecords, inspectionTaskLabel } from '../../utils/inspectionConversation'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

const ChatTranscript = lazy(() => import('../TerminalEventTranscript').then(module => ({default:module.TerminalEventTranscript})))
const ExecutionLogsPopup = lazy(() => import('../workflow/ExecutionLogsPopup'))
const modeLabels: Record<string,string> = {agent:'Agent',script:'Script',validation:'Validation'}
function ExecutionType({mode}: {mode?: string}) {
 return <span className="shrink-0 rounded border border-border bg-muted px-2 py-0.5 text-[11px] font-medium" data-testid="inspection-execution-type">{modeLabels[mode || ''] || 'Type not recorded'}</span>
}

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

function AgentConversation({data}: {data: ExecutionInspectionResponse}) {
 const records = useMemo(() => inspectionRecords(data),[data])
 const chat = useMemo(() => inspectionChat(data),[data])
 const captured = data.transcript.events.some(event => event.tool_call || ['system','user','assistant'].includes(event.role || ''))
 return <div className="flex h-full min-h-0 flex-col">
  <details className="mx-3 mb-2 shrink-0 rounded border border-border p-2 text-xs" data-testid="inspection-system-prompt"><summary className="cursor-pointer font-medium">System prompt <span className="ml-2 font-normal text-muted-foreground">{records.system.length ? 'Expand to debug' : 'Not recorded'}</span></summary><div className="mt-2 max-h-64 overflow-auto">{records.system.length ? records.system.map((text,index)=><pre key={index} className="whitespace-pre-wrap break-words pb-3 text-xs">{text}</pre>) : <p className="text-muted-foreground">No system prompt was recorded for this execution.</p>}</div></details>
  {!captured && data.transcript.status !== 'running' && <p className="mx-3 mb-2 shrink-0 text-xs text-muted-foreground">No conversation was recorded for this execution. A new run with the updated backend is needed to capture messages and tools.</p>}
  <Suspense fallback={<p className="p-3 text-xs">Loading conversation…</p>}><ChatTranscript events={chat.events} terminal={chat.terminal} scrollKey={`inspection:${data.transcript.session_id}:${data.transcript.agent_id}`} surfaceClassName="bg-background" /></Suspense>
 </div>
}

function ScriptExecution({data}: {data: ExecutionInspectionResponse}) {
 const scripts = data.scripts || []
 const hasAgent = data.transcript.events.some(event => ['system','user','assistant'].includes(event.role || ''))
 return <div className="h-full min-h-0 overflow-auto px-3 pb-3">
  {!scripts.length && <p className="mb-3 text-xs text-muted-foreground">{data.transcript.status==='running' ? 'The script will appear when it is dispatched.' : 'No script snapshot was recorded for this execution. Saved logs may contain evidence.'}</p>}
  {scripts.map((script,index)=><section key={script.started_at} className="mb-4 rounded border border-border p-3">
   <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs"><span className="font-medium">{scripts.length>1 ? `Script attempt ${index+1}` : 'Script execution'}</span><span className="text-muted-foreground">{script.origin==='completion_snapshot' ? 'Source saved after the agent turn' : `${script.status}${script.exit_code!==undefined ? ` · Exit code ${script.exit_code}` : ''}`}</span></div>
   <details className="mb-3 text-xs"><summary className="cursor-pointer font-medium">Full script <span className="ml-1 break-all font-normal text-muted-foreground">{script.path}</span></summary>{script.source ? <pre className="mt-2 whitespace-pre-wrap break-words rounded bg-muted p-3 text-xs">{script.source}</pre> : <p className="mt-2 text-muted-foreground">Source could not be captured at dispatch.</p>}</details>
   <h3 className="mb-2 text-xs font-medium">Output</h3><pre className="whitespace-pre-wrap break-words text-xs">{script.stdout || (script.status==='running' ? 'Running…' : 'No standard output was recorded.')}</pre>
   {script.stderr && <><h3 className="mb-2 mt-3 text-xs font-medium">Standard error</h3><pre className="whitespace-pre-wrap break-words text-xs">{script.stderr}</pre></>}
   {script.error && <pre className="mt-3 whitespace-pre-wrap break-words text-xs text-destructive">{script.error}</pre>}
  </section>)}
  <section className="mb-4"><h3 className="mb-2 text-sm font-medium">Final output</h3>{data.result ? <MarkdownRenderer content={data.result} /> : <p className="text-xs text-muted-foreground">{data.transcript.status==='running' ? 'The final output will appear when this execution finishes.' : 'No final output was recorded.'}</p>}</section>
  {hasAgent && <details className="border-t border-border pt-3 text-xs"><summary className="cursor-pointer">Agent repair conversation</summary><div className="mt-3 h-[32rem] min-h-0"><AgentConversation data={data} /></div></details>}
 </div>
}

function ExecutionRecords({data}: {data: ExecutionInspectionResponse}) {
 return <div className="flex h-full min-h-0 flex-col">
  {data.transcript.error && <p className="mx-3 mb-3 shrink-0 whitespace-pre-wrap text-xs text-destructive">{data.transcript.error}</p>}
  {data.execution_mode==='script' ? <ScriptExecution data={data} /> : data.execution_mode==='validation' && !data.transcript.events.some(event=>['system','user','assistant'].includes(event.role || '')) ? <div className="overflow-auto px-3 text-xs"><h3 className="mb-2 text-sm font-medium">Validation result</h3>{data.result ? <MarkdownRenderer content={data.result} /> : <p>Validation details were not recorded.</p>}</div> : <AgentConversation data={data} />}
 </div>
}

function TaskRecords({target}: {target: ExecutionInspectionTarget}) {
 const {data, error, refresh} = useExecutionRecords(target)
 return <div className="flex h-full min-h-0 flex-col"><div className="mb-3 flex shrink-0 items-center gap-2 px-3"><ExecutionType mode={data?.execution_mode} /><h3 className="min-w-0 flex-1 truncate text-xs font-medium">{inspectionTaskLabel(target.name)}</h3><span className="text-xs text-muted-foreground">{data?.transcript.status || 'Loading…'}</span><button type="button" aria-label="Refresh task inspection" title="Refresh task" onClick={refresh} className="rounded p-1 text-muted-foreground hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" /></button></div>{error && <p role="status" className="mb-3 px-3 text-xs text-amber-600">{error}</p>}{data ? <ExecutionRecords data={data} /> : <p className="px-3 text-xs text-muted-foreground">Loading task records…</p>}</div>
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
 const selectedID = chosen ?? (data?.execution_mode!=='script' && !transcript?.events.some(event=>event.tool_call || ['system','user','assistant'].includes(event.role || '')) ? preferred?.agent_id : undefined) ?? target.execution_id
 const child = children.find(item => item.agent_id === selectedID)
 const childTarget = useMemo(() => child ? {workspace_path: target.workspace_path, session_id: target.session_id, execution_id: child.agent_id, name: child.name, started_at: child.started_at || target.started_at} : null, [target, child?.agent_id, child?.name, child?.started_at])
 const labelsId = useId()
 return <div className="flex h-full min-h-0 flex-col">
  <div className="shrink-0 border-b border-border px-3 py-2">
   <div className="flex items-center gap-2"><ExecutionType mode={data?.execution_mode} /><h2 className="min-w-0 flex-1 truncate text-sm font-medium" title={transcript?.name || target.name}>{transcript?.name || target.name || 'Step'}</h2><span className="text-xs text-muted-foreground">{transcript?.status || 'Loading…'}</span><button type="button" aria-label="Refresh step inspection" title="Refresh step" onClick={refresh} className="rounded p-1 text-muted-foreground hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" /></button></div>
   {children.length > 0 && <div className="mt-3 flex items-center gap-2"><label htmlFor={`${labelsId}-task`} className="shrink-0 text-xs text-muted-foreground">Inspect task</label><select id={`${labelsId}-task`} value={selectedID} onChange={event => setChosen(event.target.value)} className="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1.5 text-xs"><option value={target.execution_id}>Step result · {transcript?.status}</option>{children.map(item => <option key={item.agent_id} value={item.agent_id}>{inspectionTaskLabel(item.name)} · {item.status}</option>)}</select></div>}
  </div>
  <div className="flex min-h-0 flex-1 flex-col pt-3">
   {error && <div role="status" className="mb-3 flex gap-2 text-xs text-amber-600"><AlertCircle className="h-4 w-4 shrink-0" />{error}</div>}
   {childTarget ? <TaskRecords key={childTarget.execution_id} target={childTarget} /> : data ? <ExecutionRecords data={data} /> : <p className="text-xs text-muted-foreground">Loading execution records…</p>}
   <details className="mt-2 max-h-[28rem] shrink-0 overflow-auto border-t border-border p-3 text-xs"><summary className="cursor-pointer text-muted-foreground">Technical details and saved logs</summary><dl className="my-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]"><dt>Execution</dt><dd className="break-all">{selectedID}</dd>{actual.step_id && <><dt>Step</dt><dd>{actual.step_id}</dd></>}{actual.run_folder && <><dt>Run folder</dt><dd>{actual.run_folder}</dd></>}<dt>Started</dt><dd>{new Date(childTarget?.started_at || transcript?.started_at || target.started_at).toLocaleString()}</dd></dl>
    {actual.run_folder && actual.step_id && <><button type="button" onClick={() => setLogsOpen(value => !value)} className="rounded border border-border px-2 py-1.5">{logsOpen ? 'Hide saved logs' : 'Open saved logs'}</button>{logsOpen && <div className="mt-3 h-96 min-h-0"><p className="mb-2 text-[11px] text-muted-foreground">Saved attempts for this step. These may include other executions; check their timestamps.</p><Suspense fallback={<p>Loading saved logs…</p>}><ExecutionLogsPopup workspacePath={target.workspace_path} runFolder={actual.run_folder} runFolders={[actual.run_folder]} initialStepId={actual.step_id} allowDefaultRunFolder={false} /></Suspense></div>}</>}
   </details>
  </div>
 </div>
}

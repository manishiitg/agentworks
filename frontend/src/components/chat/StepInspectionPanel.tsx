import { lazy, Suspense, useEffect, useId, useMemo, useState } from 'react'
import { AlertCircle, Check, ChevronRight, RefreshCw, Settings2 } from 'lucide-react'
import type { ExecutionInspectionTarget, ExecutionInspectionResponse } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { inspectionChat, inspectionHasConversation, inspectionRecords, inspectionTaskLabel } from '../../utils/inspectionConversation'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

const ChatTranscript = lazy(() => import('../TerminalEventTranscript').then(module => ({default:module.TerminalEventTranscript})))
const modeLabels: Record<string,string> = {agent:'Agent',script:'Script',validation:'Validation'}
function ExecutionType({mode}: {mode?: string}) {
 return <span className="shrink-0 rounded border border-border px-2 py-0.5 text-[11px] font-medium" data-testid="inspection-execution-type">{modeLabels[mode || ''] || 'Type not recorded'}</span>
}

function useExecutionRecords(target: ExecutionInspectionTarget | null, reload = 0) {
 const [data, setData] = useState<ExecutionInspectionResponse | null>(null)
 const [error, setError] = useState('')
 const [refresh, setRefresh] = useState(0)
 useEffect(() => {
  if (!target) {setData(null);setError('');return}
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
    if (status === 404 && ++missingReads >= 3) {setError('No saved records are available for this execution. Try Refresh.'); return}
    if (status === 401 || status === 403) {setData(null); setError('Access to this execution is no longer available'); return}
    setError('Execution records are temporarily unavailable. Retrying…')
   }
   if (!disposed && isChatIdentityCurrent(identity) && terminalReads < 3) timer = setTimeout(() => {void load()}, 2500)
  }
  void load()
  return () => {disposed = true; clearTimeout(timer)}
 }, [target, refresh, reload])
 // Never show a prior execution while an identity change starts its request.
 const current = target && data?.transcript.agent_id === target.execution_id && data.transcript.session_id === target.session_id ? data : null
 return {data: current, error, refresh: () => setRefresh(value => value + 1)}
}

function AgentConversation({data}: {data: ExecutionInspectionResponse}) {
 const records = useMemo(() => inspectionRecords(data),[data])
 const chat = useMemo(() => inspectionChat(data),[data])
 const captured = inspectionHasConversation(data)
 return <div className="flex h-full min-h-0 flex-col">
  <details className="group mx-3 mb-2 shrink-0 text-xs" data-testid="inspection-system-prompt"><summary className="flex cursor-pointer list-none items-center gap-2 py-1.5 text-muted-foreground [&::-webkit-details-marker]:hidden"><Settings2 className="h-3.5 w-3.5" aria-hidden="true" />System prompt{!records.system.length && <span className="ml-2">Not recorded</span>}<ChevronRight className="ml-auto h-3.5 w-3.5 transition-transform group-open:rotate-90" aria-hidden="true" /></summary><div className="mt-2 max-h-64 overflow-auto rounded bg-muted/40 p-3">{records.system.length ? records.system.map((text,index)=><pre key={index} className="whitespace-pre-wrap break-words pb-3 text-xs">{text}</pre>) : <p className="text-muted-foreground">No system prompt was recorded for this execution.</p>}</div></details>
  {!captured && data.transcript.status !== 'running' && <p className="mx-3 mb-2 shrink-0 text-xs text-muted-foreground">No conversation is attached to this execution record. Messages may be saved separately in task records whose links are missing.</p>}
  <Suspense fallback={<p className="p-3 text-xs">Loading conversation…</p>}><ChatTranscript events={chat.events} terminal={chat.terminal} scrollKey={`inspection:${data.transcript.session_id}:${data.transcript.agent_id}`} surfaceClassName="bg-background" toolLayout="rows" /></Suspense>
 </div>
}

function ScriptExecution({data}: {data: ExecutionInspectionResponse}) {
 const scripts = data.scripts || []
 const hasAgent = inspectionHasConversation(data)
 return <div className="h-full min-h-0 overflow-auto px-3 pb-3">
  {!scripts.length && <p className="mb-3 text-xs text-muted-foreground">{data.transcript.status==='running' ? 'The script will appear when it is dispatched.' : 'No script snapshot was recorded for this execution.'}</p>}
  {scripts.map((script,index)=><section key={script.started_at} className="mb-4">
   {(scripts.length>1 || script.origin==='completion_snapshot') && <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs">{scripts.length>1 && <span className="font-medium">Script attempt {index+1}</span>}<span className="text-muted-foreground">{script.origin==='completion_snapshot' ? 'Source saved after the agent turn' : script.status}</span></div>}
   <details open={scripts.length===1} className="mb-3 text-xs"><summary className="cursor-pointer font-medium">Full script <span className="ml-1 break-all font-normal text-muted-foreground">{script.path}</span></summary>{script.source ? <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-3 text-xs">{script.source}</pre> : <p className="mt-2 text-muted-foreground">Source could not be captured at dispatch.</p>}</details>
   <div className="mb-2 flex items-center justify-between gap-2"><h3 className="text-xs font-medium">Output</h3>{script.exit_code!==undefined && <span className="text-xs text-muted-foreground">Exit code {script.exit_code}</span>}</div><pre className="whitespace-pre-wrap break-words text-xs">{script.stdout || (script.status==='running' ? 'Running…' : 'No standard output was recorded.')}</pre>
   {script.stderr && <><h3 className="mb-2 mt-3 text-xs font-medium">Standard error</h3><pre className="whitespace-pre-wrap break-words text-xs">{script.stderr}</pre></>}
   {script.error && <pre className="mt-3 whitespace-pre-wrap break-words text-xs text-destructive">{script.error}</pre>}
  </section>)}
  <section className="mb-4"><h3 className="mb-2 text-sm font-medium">Final output</h3>{data.result ? <MarkdownRenderer content={data.result} /> : <p className="text-xs text-muted-foreground">{data.transcript.status==='running' ? 'The final output will appear when this execution finishes.' : 'No final output was recorded.'}</p>}</section>
  {hasAgent && <details className="border-t border-border pt-3 text-xs"><summary className="cursor-pointer">Agent repair conversation</summary><div className="mt-3 h-[32rem] min-h-0"><AgentConversation data={data} /></div></details>}
 </div>
}

function ExecutionRecords({data}: {data: ExecutionInspectionResponse}) {
 const hasConversation = inspectionHasConversation(data)
 if (data.execution_mode==='agent' && data.children.length>0 && !hasConversation) return <section className="overflow-auto px-3 text-xs"><h3 className="mb-2 text-sm font-medium">Step result</h3>{data.transcript.error && <p className="mb-3 whitespace-pre-wrap text-destructive">{data.transcript.error}</p>}{data.result ? <MarkdownRenderer content={data.result} /> : <p className="text-muted-foreground">{data.transcript.status==='running' ? 'The step result will appear when its tasks finish.' : 'No overall result was recorded.'}</p>}</section>
 return <div className="flex h-full min-h-0 flex-col">
  {data.transcript.error && <p className="mx-3 mb-3 shrink-0 whitespace-pre-wrap text-xs text-destructive">{data.transcript.error}</p>}
  {data.execution_mode==='script' ? <ScriptExecution data={data} /> : data.execution_mode==='validation' && !data.transcript.events.some(event=>['system','user','assistant'].includes(event.role || '')) ? <div className="overflow-auto px-3 text-xs"><h3 className="mb-2 text-sm font-medium">Validation result</h3>{data.result ? <MarkdownRenderer content={data.result} /> : <p>Validation details were not recorded.</p>}</div> : <AgentConversation data={data} />}
 </div>
}

function SavedExecutionLog({data, label}: {data: ExecutionInspectionResponse; label: string}) {
 const [open, setOpen] = useState(false)
 return <details data-testid="inspection-saved-log" onToggle={event=>setOpen(event.currentTarget.open)} className="border-t border-border pt-3"><summary className="cursor-pointer text-muted-foreground">Saved execution log</summary>{open && <><p className="my-2 text-[11px] text-muted-foreground">Saved records for {label}.</p><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-muted/40 p-3 text-[11px]">{JSON.stringify({execution_mode:data.execution_mode,transcript:data.transcript,conversation:data.conversation,scripts:data.scripts,result:data.result},null,2)}</pre></>}</details>
}

function isSubAgent(item: ExecutionInspectionResponse['children'][number]) {
 return ['sub_agent','workflow_sub_agent','workflow_generic_agent'].includes(item.kind || '')
}

export default function StepInspectionPanel({target}: {target: ExecutionInspectionTarget}) {
 const {data, error, refresh} = useExecutionRecords(target)
 const scope = `${target.workspace_path}:${target.session_id}:${target.execution_id}`
 const [navigation, setNavigation] = useState<{scope: string; chosen: string | null; nested: ExecutionInspectionTarget[]}>({scope,chosen:null,nested:[]})
 const chosen = navigation.scope === scope ? navigation.chosen : null
 const nested = navigation.scope === scope ? navigation.nested : []
 const choose = (id: string) => setNavigation({scope,chosen:id,nested:[]})
 const [reload, setReload] = useState(0)
 const transcript = data?.transcript
 const actual = transcript?.inspection ?? target
 const children = data?.children ?? []
 // Delegating parents have no provider conversation. Open an actionable task
 // immediately, while allowing explicit selection of the step's own result.
 const workTasks = children.filter(child => !/\(prevalidation\)$/.test(child.name))
 const finalValidation = children.find(child => inspectionTaskLabel(child.name)==='Final validation')
 const preferred = workTasks.find(child => child.status === 'failed') || workTasks.find(child => child.status === 'running') || workTasks[0]
 const hasConversation = data ? inspectionHasConversation(data) : false
 const selectedID = chosen ?? (data?.execution_mode!=='script' && !hasConversation ? preferred?.agent_id : undefined) ?? target.execution_id
 const child = children.find(item => item.agent_id === selectedID)
 const childTarget = useMemo(() => child ? {workspace_path: target.workspace_path, session_id: target.session_id, execution_id: child.agent_id, name: child.name, started_at: child.started_at || target.started_at} : null, [target, child?.agent_id, child?.name, child?.started_at])
 const activeTarget = nested.at(-1) ?? childTarget
 const {data: selectedData, error: selectedError} = useExecutionRecords(activeTarget, reload)
 const viewedData = activeTarget ? selectedData : data
 const viewedTranscript = viewedData?.transcript
 const viewedID = activeTarget?.execution_id || target.execution_id
 const viewedName = activeTarget?.name || transcript?.name || target.name
 const primaryTasks = hasConversation ? children.filter(isSubAgent) : workTasks
 const seen = new Set([target.execution_id,childTarget?.execution_id,...nested.map(item=>item.execution_id)])
 const nestedAgents = activeTarget ? (viewedData?.children ?? []).filter(item=>isSubAgent(item) && !seen.has(item.agent_id)) : []
 const openNested = (id: string) => {
  const item = nestedAgents.find(record=>record.agent_id===id)
  if (item && nested.length<16) setNavigation({scope,chosen:selectedID,nested:[...nested,{workspace_path:target.workspace_path,session_id:target.session_id,execution_id:item.agent_id,name:item.name,started_at:item.started_at || activeTarget?.started_at || target.started_at}]})
 }
 const labelsId = useId()
 return <div className="flex h-full min-h-0 flex-col">
  <div className="shrink-0 border-b border-border px-3 py-2">
   <div className="flex items-center gap-2"><ExecutionType mode={viewedData?.execution_mode} /><h2 className="min-w-0 flex-1 truncate text-sm font-medium" title={viewedName}>{viewedName || 'Step'}</h2><span data-testid="inspection-status" className={`inline-flex shrink-0 items-center gap-1 text-xs capitalize ${viewedTranscript?.status==='completed' ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>{viewedTranscript?.status==='completed' && <Check className="h-3 w-3" aria-hidden="true" />}{viewedTranscript?.status || 'Loading…'}</span><button type="button" aria-label="Refresh step inspection" title="Refresh step" onClick={()=>{refresh();setReload(value=>value+1)}} className="rounded p-1 text-muted-foreground hover:bg-muted"><RefreshCw className="h-3.5 w-3.5" /></button></div>
   {(hasConversation ? primaryTasks.length>0 : primaryTasks.length>1) && <select aria-label="Task" value={selectedID} onChange={event=>choose(event.target.value)} className="mt-2 max-w-full rounded border border-border bg-background px-2 py-1 text-xs"><option value={target.execution_id}>Step conversation</option>{primaryTasks.map(item=><option key={item.agent_id} value={item.agent_id}>{inspectionTaskLabel(item.name)}</option>)}</select>}
   {nested.length>0 && <nav aria-label="Inspection path" className="mt-2 flex flex-wrap gap-2 text-xs"><button onClick={()=>setNavigation({scope,chosen:selectedID,nested:[]})} className="text-muted-foreground underline">{child?.name || 'Step'}</button>{nested.map((item,index)=><button key={item.execution_id} onClick={()=>setNavigation({scope,chosen:selectedID,nested:nested.slice(0,index+1)})} className="text-muted-foreground underline">{item.name}</button>)}</nav>}
   {nestedAgents.length>0 && <select aria-label="Nested sub-agent" value="" onChange={event=>openNested(event.target.value)} className="mt-2 max-w-full rounded border border-border bg-background px-2 py-1 text-xs"><option value="">Inspect a nested sub-agent…</option>{nestedAgents.map(item=><option key={item.agent_id} value={item.agent_id}>{item.name}</option>)}</select>}
  </div>
  <div className="flex min-h-0 flex-1 flex-col pt-3">
   {error && <div role="status" className="mb-3 flex gap-2 text-xs text-amber-600"><AlertCircle className="h-4 w-4 shrink-0" />{error}</div>}
   {selectedError && <p role="status" className="mb-3 px-3 text-xs text-amber-600">{selectedError}</p>}
   {viewedData?.conversation?.truncated && <p role="status" className="mb-3 shrink-0 px-3 text-xs text-muted-foreground">Showing part of the recorded conversation. Inspect an individual task in Run details for its saved messages.</p>}
   {viewedData ? <ExecutionRecords data={viewedData} /> : <p className="px-3 text-xs text-muted-foreground">Loading conversation…</p>}
   <details data-testid="inspection-technical-details" className="mt-2 max-h-[28rem] shrink-0 overflow-auto border-t border-border p-3 text-xs"><summary className="cursor-pointer text-muted-foreground">Run details</summary>
    {children.length>0 && <div className="mt-3 flex items-center gap-2"><label htmlFor={`${labelsId}-task`} className="shrink-0 text-xs text-muted-foreground">Inspect task</label><select id={`${labelsId}-task`} value={selectedID} onChange={event=>choose(event.target.value)} className="min-w-0 flex-1 rounded border border-border bg-background px-2 py-1.5 text-xs"><option value={target.execution_id}>Step result</option>{children.map(item=><option key={item.agent_id} value={item.agent_id}>{inspectionTaskLabel(item.name)}</option>)}</select></div>}
    <dl className="my-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">{finalValidation && <><dt>Overall step validation</dt><dd>{finalValidation.status==='completed' ? 'Passed' : finalValidation.status==='failed' ? 'Failed' : finalValidation.status}</dd></>}<dt>Viewing</dt><dd>{activeTarget ? inspectionTaskLabel(viewedName) : 'Step conversation'}</dd><dt>Execution</dt><dd className="break-all">{viewedID}</dd>{actual.step_id && <><dt>Step</dt><dd>{actual.step_id}</dd></>}{actual.run_folder && <><dt>Run folder</dt><dd>{actual.run_folder}</dd></>}<dt>Started</dt><dd>{new Date(activeTarget?.started_at || transcript?.started_at || target.started_at).toLocaleString()}</dd></dl>
    {viewedData && <SavedExecutionLog key={viewedID} data={viewedData} label={activeTarget ? inspectionTaskLabel(viewedName) : 'this step'} />}
   </details>
  </div>
 </div>
}

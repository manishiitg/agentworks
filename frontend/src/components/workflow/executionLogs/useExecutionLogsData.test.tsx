// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { agentApi } from '../../../services/api'
import { useExecutionLogsData } from './useExecutionLogsData'
import type { ExecutionLogsResponse } from '../../../services/api-types'

it('clears prior run evidence immediately and ignores its late file response', async () => {
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
 let finishRun!: (data: ExecutionLogsResponse) => void
 let finishFile!: (data: string) => void
 vi.spyOn(agentApi, 'getExecutionLogs').mockImplementation((_, run) => run === 'iteration-1/default'
  ? Promise.resolve({success:true,steps:{research:{step_id:'research',title:'Old run',type:'agent',description:'Read sources',validations:[],executions:[]}}})
  : new Promise(resolve => {finishRun = resolve}))
 vi.spyOn(agentApi, 'getLogFile').mockImplementation(() => new Promise(resolve => {finishFile = resolve}))
 function Probe({run}: {run:string}) {
  const data = useExecutionLogsData({workspacePath:'Workflow/example',initialRunFolder:run,runFolders:[run],initialStepId:'research'})
  return <div><pre>{JSON.stringify({logs:data.logs,files:data.fileContents})}</pre><button onClick={() => data.toggleFileExpansion('Workflow/example/iteration-1/default/conversation.json')}>Open file</button></div>
 }
 const host = document.createElement('div'); const root = createRoot(host)
 try {
  await act(async () => root.render(<Probe run="iteration-1/default" />))
  expect(host.textContent).toContain('Old run')
  await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
  await act(async () => root.render(<Probe run="iteration-2/default" />))
  expect(host.textContent).not.toContain('Old run')
  await act(async () => finishFile('Old file content'))
  expect(host.textContent).not.toContain('Old file content')
  await act(async () => finishRun({success:true,steps:{}}))
  expect(host.textContent).toContain('"steps":{}')
 } finally {await act(async () => root.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals()}
})

// @vitest-environment happy-dom
import React, {act} from 'react'
import {createRoot} from 'react-dom/client'
import {afterEach,expect,it,vi} from 'vitest'
import {TeamSchedules} from './TeamSchedules'
import {teamRequest} from './teamApi'
import {schedulerApi} from '../../api/scheduler'
vi.mock('./teamApi',()=>({teamRequest:vi.fn(),teamFailure:(e:Error)=>({message:e.message})}))
vi.mock('../../api/scheduler',()=>({schedulerApi:{listJobs:vi.fn(),createJob:vi.fn(),enableJob:vi.fn()}}))
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
const button=(text:string)=>[...host.querySelectorAll('button')].find(b=>b.textContent?.includes(text)||b.getAttribute('aria-label')?.includes(text))!
afterEach(async()=>{await act(async()=>root.render(null));vi.clearAllMocks()})

// #3282 Sharing and adding a schedule is optional both ways. Share sends only the name, timing and message of a repeating
// message schedule (never a workflow); Add creates it in your own Crew switched off; your own shared schedule has no Add.
it('shares only a repeating message schedule and adds a teammate schedule switched off',async()=>{
 const file={kind:'schedule',name:'Monday summary',cron_expression:'0 9 * * 1',timezone:'UTC',message:'Summarise last week.',when:'Every Monday at 9:00 AM'}
 vi.mocked(teamRequest).mockImplementation(async(_t,suffix,method)=>{
  if(suffix==='/files')return {files:[{path:'docs/schedules/aaa.json',hash:'h1',author:'Colleague',mine:false},{path:'docs/schedules/bbb.json',hash:'h2',mine:true},{path:'docs/notes.md',hash:'h3'}]}
  if(method==='GET')return JSON.stringify(suffix?.includes('aaa')?file:{...file,name:'Release check'})
  return ''
 })
 const job={id:'product-project:work:crew:s1',entity_type:'product',name:'Standup',description:'',cron_expression:'30 9 * * 1-5',timezone:'UTC',schedule_type:'cron',messages:['Post the standup.'],timing_summary:'Weekdays at 9:30 AM'}
 vi.mocked(schedulerApi.listJobs).mockResolvedValue({jobs:[job,{...job,id:'product-project:work:crew:w1',name:'Workflow run',target_workflow_id:'wf'},{...job,id:'product-project:work:crew:m1',name:'Two messages',messages:['a','b']},{...job,id:'product-project:work:other:s9',name:'Other crew'}],total:4,limit:200,offset:0} as never)
 vi.mocked(schedulerApi.createJob).mockResolvedValue({id:'product-project:work:crew:new'} as never)
 await act(async()=>{root.render(<TeamSchedules target={{project:'crew',folderId:'f'}} folder="docs"/>);for(let i=0;i<6;i++)await Promise.resolve()})
 expect(host.textContent).toContain('Shared by Colleague')
 expect(host.textContent).toContain('Shared by you')
 expect(host.querySelector('[aria-label="Add Release check to my schedules"]')).toBeNull()
 expect([...host.querySelectorAll('select[aria-label="Schedule to share"] option')].map(o=>o.textContent)).toEqual(['Standup'])
 await act(async()=>button('Share with the Team').click())
 const put=vi.mocked(teamRequest).mock.calls.find(call=>call[2]==='PUT')!
 expect(JSON.parse(put[3] as string)).toEqual({kind:'schedule',name:'Standup',cron_expression:'30 9 * * 1-5',timezone:'UTC',message:'Post the standup.',when:'Weekdays at 9:30 AM'})
 await act(async()=>button('Add Monday summary to my schedules').click())
 await act(async()=>button('Add, switched off').click())
 expect(schedulerApi.createJob).toHaveBeenCalledWith(expect.objectContaining({entity_type:'product',product_profile_id:'work',product_project_id:'crew',name:'Monday summary',messages:['Summarise last week.'],cron_expression:'0 9 * * 1',enabled:false}))
 await act(async()=>button('Turn it on').click())
 expect(schedulerApi.enableJob).toHaveBeenCalledWith('product-project:work:crew:new')
})

import { useEffect, useRef, useState } from 'react'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { teamRequest, type TeamTarget } from './teamApi'
import type { TeamSetupList } from './TeamSetups'

/** The same bounded list/count read for the compact link and full Team tab. */
export function useTeamSetupsList(target:TeamTarget|undefined,projectId:string,enabled:boolean) {
 const scope=JSON.stringify([captureChatIdentity(),projectId,target?.project,target?.connection])
 const [state,setState]=useState<{scope:string;data?:TeamSetupList;version:number;error:string}>()
 const refresh=useRef(()=>{})
 useEffect(()=>{
  if(!target||!enabled)return
  let active=true,reading=false
  const identity=captureChatIdentity()
  const read=async()=>{
   if(reading)return
   reading=true
   try {
    const data=await teamRequest<TeamSetupList>(target,'/checklists')
    if(active&&isChatIdentityCurrent(identity))setState(old=>({scope,data,version:(old?.version||0)+1,error:''}))
   } catch {
    if(active&&isChatIdentityCurrent(identity))setState({scope,version:0,error:'Could not load the Team kit. Try again.'})
   } finally {reading=false}
  }
  refresh.current=()=>{void read()};void read()
  window.addEventListener('agentworks:team-updated',read)
  return()=>{active=false;refresh.current=()=>{};window.removeEventListener('agentworks:team-updated',read)}
 },[scope,enabled,target?.project,target?.connection])
 useLiveRefetch(()=>refresh.current(),{kinds:['crew_panel'],project:projectId,scopeKey:scope,enabled:enabled&&!!target,fallbackMs:15000,safetyMs:60000,minIntervalMs:3000})
 return state?.scope===scope&&enabled?state:{data:undefined,version:0,error:''}
}

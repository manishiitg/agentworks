import { useEffect, useRef, useState } from 'react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { chatRuntimeActivity } from '../../utils/chatRuntimeActivity'
import type { AppearanceDecoration, AppearanceShader } from '../../stores/appearanceTheme'
import { mountAppearanceShader } from './shaderEngine'

function DecorationNodes({nodes}:{nodes:AppearanceDecoration[]}) {
 return <>{nodes.map(node=><div key={node.id} data-theme-decoration={node.id}>{node.children&&<DecorationNodes nodes={node.children}/>}</div>)}</>
}
/** Authenticated personal decorations, never arbitrary HTML/React or active UI. */
export default function AppearanceBackground() {
 const actor=useAuthStore(state=>state.user?.id),server=useWorkspaceConnectionStore(state=>state.activeWorkspaceId)
 const scope=useAppearanceStore(state=>state.scope),theme=useAppearanceStore(state=>state.preview??state.current),profile=useAppearanceStore(state=>state.profile)
 const working=useChatStore(state=>{
  const tab=state.activeTabId?state.chatTabs[state.activeTabId]:undefined
  return !!tab&&chatRuntimeActivity(tab,state.activeSessionsCache.find(row=>row.session_id===tab.sessionId)).state==='running'
 })
 if(!actor||scope!==JSON.stringify([server,actor]))return null
 const accent=theme.colors[profile]?.accent??(profile==='dark'?'#60a5fa':'#2563eb')
 return <>{theme.background_shader&&<PersonalShader key={JSON.stringify([actor,server,theme.background_shader])} shader={theme.background_shader} accent={accent} working={working}/>}
 {!!theme.decorations?.length&&<div data-theme-decorations aria-hidden inert><DecorationNodes nodes={theme.decorations}/></div>}</>
}
function PersonalShader({shader,accent,working}:{shader:AppearanceShader;accent:string;working:boolean}) {
 const canvas=useRef<HTMLCanvasElement>(null),engine=useRef<ReturnType<typeof mountAppearanceShader>>(null)
 const [ready,setReady]=useState(false)
 useEffect(()=>{
  const mounted=mountAppearanceShader(canvas.current!,shader,(state,ms)=>{
   if(state==='ready') {setReady(true);document.documentElement.dataset.appearanceShader=''}
   if(state==='stopped'||state==='failed'){setReady(false);delete document.documentElement.dataset.appearanceShader}
   if(state==='stopped'||state==='throttled')console.info(`[APPEARANCE_SHADER] outcome=${state} ms=${Math.round(ms??0)}`)
  })
  engine.current=mounted
  return ()=>{mounted?.destroy();engine.current=null;delete document.documentElement.dataset.appearanceShader}
 },[shader.code,shader.speed,shader.intensity])
 useEffect(()=>{engine.current?.setUniforms([1,3,5].map(i=>parseInt(accent.slice(i,i+2),16)/255) as [number,number,number],working)},[accent,working])
 return <div data-theme-shader aria-hidden style={{visibility:ready?'visible':'hidden'}}><canvas ref={canvas}/></div>
}

import { useEffect, useRef, useState } from 'react'
import type { AppearanceScript } from '../../stores/appearanceTheme'
import { mountScriptSandbox } from './scriptSandbox'

export function PersonalScript({script,colors,working}:{script:AppearanceScript;colors:Record<string,string>;working:boolean}) {
 const host=useRef<HTMLDivElement>(null),latest=useRef({colors,working})
 latest.current={colors,working}
 const [ready,setReady]=useState(false)
 useEffect(()=>{
  const motion=matchMedia('(prefers-reduced-motion: reduce)')
  let engine:ReturnType<typeof mountScriptSandbox>|undefined
  const refresh=()=>{
   engine?.destroy();engine=undefined;setReady(false);delete document.documentElement.dataset.appearanceScript
   if(!document.hidden) engine=mountScriptSandbox(host.current!,script,()=>latest.current,state=>{
    if(state==='ready'){setReady(true);document.documentElement.dataset.appearanceScript=''}
    if(state==='failed'||state==='stopped'){setReady(false);delete document.documentElement.dataset.appearanceScript}
    if(state==='failed'||state==='stopped'||state==='throttled')console.info(`[APPEARANCE_SCRIPT] outcome=${state}`)
   })
  }
  refresh();document.addEventListener('visibilitychange',refresh);motion.addEventListener('change',refresh);window.addEventListener('resize',refresh)
  return ()=>{engine?.destroy();delete document.documentElement.dataset.appearanceScript;document.removeEventListener('visibilitychange',refresh);motion.removeEventListener('change',refresh);window.removeEventListener('resize',refresh)}
 },[script.code,script.fps])
 return <div data-theme-script ref={host} aria-hidden inert style={{visibility:ready?'visible':'hidden'}}/>
}

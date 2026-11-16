import { useEffect, useState } from 'react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import type { AppearanceImage as ImageSettings } from '../../stores/appearanceTheme'
import api from '../../services/api'
export function AppearanceImage(){
 const actor=useAuthStore(s=>s.user?.id),server=useWorkspaceConnectionStore(s=>s.activeWorkspaceId),scope=useAppearanceStore(s=>s.scope)
 const image=useAppearanceStore(s=>(s.preview??s.current).background_image)
 if(!actor||scope!==JSON.stringify([server,actor])||!image)return null
 return <PersonalImage key={JSON.stringify([actor,server,image.asset_id])} image={image}/>
}
function PersonalImage({image}:{image:ImageSettings}){
 const [url,setURL]=useState('')
 useEffect(()=>{
  const controller=new AbortController();let owned:string|undefined;const decoded=new Image()
  void api.get<Blob>(`/api/appearance/images/${image.asset_id}`,{responseType:'blob',skipSessionContext:true,signal:controller.signal}).then(({data})=>{
   if(controller.signal.aborted||data.size>5*1024*1024||!['image/png','image/jpeg'].includes(data.type))return
   owned=URL.createObjectURL(data)
   decoded.onload=()=>{if(!controller.signal.aborted){setURL(owned!);document.documentElement.dataset.appearanceImage=''}}
   decoded.src=owned
  }).catch(()=>{/* Preserve the palette/CSS fallback. */})
  return ()=>{controller.abort();decoded.onload=null;decoded.src='';if(owned)URL.revokeObjectURL(owned);delete document.documentElement.dataset.appearanceImage}
 },[image.asset_id])
 return url?<div data-theme-image aria-hidden className="pointer-events-none fixed inset-0" style={{zIndex:0,backgroundImage:`url("${url}")`,backgroundSize:image.fit==='tile'?'auto':image.fit,backgroundRepeat:image.fit==='tile'?'repeat':'no-repeat',backgroundPosition:`${image.position_x}% ${image.position_y}%`,filter:`brightness(${image.brightness})`}}/>:null
}

import { useRef, useState } from 'react'
import { ImagePlus } from 'lucide-react'
import api from '../../services/api'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
/** File selection, not a theme editor: Downloads and other chosen files work alike. */
export function AppearanceImageButton(){
 const input=useRef<HTMLInputElement>(null),[busy,setBusy]=useState(false),[error,setError]=useState('')
 const choose=async(file:File)=>{
  if(file.size>5*1024*1024){setError('Choose an image up to 5 MB.');return}
  const store=useAppearanceStore.getState(),scope=store.scope
  setBusy(true);setError('')
  try {
   const form=new FormData();form.append('image',file)
   const {data}=await api.post<{asset_id:string}>('/api/appearance/images',form,{skipSessionContext:true,headers:{'Content-Type':'multipart/form-data'}})
   if(useAppearanceStore.getState().scope!==scope)return
   const current=useAppearanceStore.getState().current
   const ok=await useAppearanceStore.getState().change('apply',{...current,background_image:{asset_id:data.asset_id,fit:'cover',position_x:50,position_y:50,brightness:current.mode==='dark'?.6:1},background_video:null,...{background_shader:null},materials:{...current.materials,page:{opacity:0}}})
   if(!ok)setError('Image imported, but the wallpaper could not be applied. Try again.')
  } catch {setError('Could not import this image. Choose a PNG, JPEG or WebP up to 5 MB.')}finally{setBusy(false)}
 }
 return <div className="space-y-2"><input ref={input} type="file" accept="image/png,image/jpeg,image/webp" className="hidden" aria-label="Wallpaper image file" onChange={e=>{const file=e.target.files?.[0];e.target.value='';if(file)void choose(file)}}/>
 <button type="button" disabled={busy} onClick={()=>input.current?.click()} className="flex min-h-9 w-full items-center gap-2 rounded-md border border-border px-3 text-sm text-foreground hover:bg-muted disabled:opacity-50"><ImagePlus className="h-4 w-4" aria-hidden/>{busy?'Preparing wallpaper…':'Choose wallpaper image…'}</button>
 <p className="text-xs text-muted-foreground">Choose a file from Downloads or another folder. Your original stays unchanged.</p>{error&&<p role="alert" className="text-xs text-destructive">{error}</p>}</div>
}

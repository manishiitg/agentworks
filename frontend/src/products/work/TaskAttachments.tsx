import { useState } from 'react'
import { PlainMarkdown } from '../../components/ui/PlainMarkdown'

/** A file sent with a request (a Needs-you card's plan or notes); the server opens it for whoever sees the request. */
export type TaskFile={id:string;name:string;size:number}
export type TaskFileContent={name:string;content_base64:string}

const image=/\.(png|jpe?g|gif|webp)$/i, text=/\.(md|markdown|txt|log|json|csv)$/i
const linkLabel=(url:string)=>{const gh=url.match(/github\.com\/[^/]+\/[^/]+\/(issues|pull)\/(\d+)/);if(gh)return gh[1]==='pull'?`PR ${gh[2]}`:`#${gh[2]}`;try{return new URL(url).hostname.replace(/^www\./,'')}catch{return url}}
const fileType=(name:string)=>image.test(name)?`image/${name.split('.').pop()!.toLowerCase().replace('jpg','jpeg')}`:'application/octet-stream'
const fileText=(base64:string)=>new TextDecoder().decode(Uint8Array.from(atob(base64),c=>c.charCodeAt(0)))

/** Links and files of a Needs-you card: links open in a new tab; an image or text file opens in place; anything else downloads. */
export function TaskAttachments({links,files,open}:{links?:string[];files?:TaskFile[];open?:(file:TaskFile)=>Promise<TaskFileContent>}) {
 const [shown,setShown]=useState<{id:string;content?:TaskFileContent;error?:string}>()
 if(!links?.length&&!files?.length)return null
 const click=async(file:TaskFile)=>{
  if(shown?.id===file.id){setShown(undefined);return}
  if(!open)return
  setShown({id:file.id})
  try{
   const content=await open(file)
   if(image.test(file.name)||text.test(file.name)){setShown({id:file.id,content});return}
   const a=document.createElement('a');a.href=`data:${fileType(file.name)};base64,${content.content_base64}`;a.download=content.name;a.click();setShown(undefined)
  }catch(e){setShown({id:file.id,error:(e as {response?:{data?:{error?:{message?:string}}}})?.response?.data?.error?.message||'This file could not be opened.'})}
 }
 const chip='rounded border border-border px-1 text-[10px] hover:bg-muted'
 return <div className="mt-0.5 space-y-1">
  <div className="flex flex-wrap gap-1">
   {links?.map(url=><a key={url} href={url} target="_blank" rel="noreferrer" title={url} className={`${chip} text-primary`}>{linkLabel(url)} ↗</a>)}
   {files?.map(file=><button key={file.id} type="button" aria-expanded={shown?.id===file.id} title={`${file.name} · ${Math.max(1,Math.round(file.size/1024))} KB`} onClick={()=>void click(file)} className={chip}>{image.test(file.name)?'🖼':'📄'} {file.name}</button>)}
  </div>
  {shown&&<div className="rounded border border-border p-1 text-xs">
   {shown.error?<p role="alert">{shown.error}</p>:!shown.content?<p>Opening…</p>:image.test(shown.content.name)?<img alt={shown.content.name} src={`data:${fileType(shown.content.name)};base64,${shown.content.content_base64}`} className="max-h-72 max-w-full rounded"/>
    :/\.(md|markdown)$/i.test(shown.content.name)?<PlainMarkdown content={fileText(shown.content.content_base64)} className="max-h-96 overflow-auto"/>
    :<pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words">{fileText(shown.content.content_base64)}</pre>}
  </div>}
 </div>
}

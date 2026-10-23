import { injectThemeTokens } from '../workflow/reportWidgets/reportHostRuntime'

// One explicit fence, complete documents only. An unfinished streamed fence
// remains source code, even when Markdown's parser auto-closes it.
export function closedVisualFence(source: string): boolean {
  const opening = /^ {0,3}(`{3,}|~{3,})visual[^\S\r\n]*\r?\n/.exec(source)
  if (!opening) return false
  const last = source.trimEnd().split('\n').at(-1)?.trim() || ''
  return last.length >= opening[1].length && [...last].every(char => char === opening[1][0])
}

export function chatVisualDocument(html: string, channel: string): { html: string; title: string } | null {
  if (!/^\s*(?:<!doctype\s+html\s*>\s*)?<html[\s>]/i.test(html) || !/<head[\s>]/i.test(html) || !/<body[\s>]/i.test(html) || !/<\/body>\s*<\/html>\s*$/i.test(html)) return null
  const page = new DOMParser().parseFromString(html, 'text/html')
  const title = page.title.trim() || 'Chat visual'
  const dark = !document.documentElement.classList.contains('light') && document.documentElement.dataset.theme !== 'light'
  page.documentElement.classList.toggle('dark', dark)
  page.documentElement.dataset.theme = dark ? 'dark' : 'light'
  const style = page.createElement('style')
  style.textContent = `:root{color-scheme:${dark ? 'dark' : 'light'};--background:${dark ? '220 13% 6%' : '0 0% 100%'};--foreground:${dark ? '220 16% 91%' : '222 24% 16%'};--card:${dark ? '220 12% 9%' : '0 0% 100%'};--border:${dark ? '220 9% 22%' : '214 22% 88%'};--primary:200 100% 40%}*{box-sizing:border-box}body{margin:0;padding:16px;font:14px/1.6 system-ui;color:hsl(var(--foreground));background:hsl(var(--background));overflow-wrap:anywhere}button,input,select{font:inherit}svg,img,canvas{max-width:100%}`
  page.head.prepend(style)
  injectThemeTokens(document.documentElement, page)
  const script = page.createElement('script')
  script.textContent = `(()=>{const channel=${JSON.stringify(channel)};const send=(kind,height)=>parent.postMessage({channel,kind,height},'*');addEventListener('error',()=>send('error'));addEventListener('unhandledrejection',()=>send('error'));addEventListener('securitypolicyviolation',()=>send('error'));addEventListener('DOMContentLoaded',()=>{const size=()=>{let bottom=0;for(const el of document.body.children)bottom=Math.max(bottom,el.getBoundingClientRect().bottom+scrollY);send('size',bottom+16)};new ResizeObserver(size).observe(document.body);size();send('ready')},{once:true})})()`
  page.head.prepend(script)
  const child = '<!doctype html>' + page.documentElement.outerHTML
  // The existing HTTP-CSP dashboard frame hosts this trusted wrapper. Its
  // frame-src 'none' also governs navigation of the inner srcdoc frame, while
  // each script-only sandbox has a separate opaque origin. No report API is
  // installed. Only geometry/lifecycle signals cross the boundary.
  const wrapper = `<!doctype html><html><head><meta charset="utf-8"><style>html,body{margin:0}iframe{display:block;width:100%;height:320px;border:0}</style></head><body><script>(()=>{const channel=${JSON.stringify(channel)};const frame=document.createElement('iframe');frame.title=${JSON.stringify(title).replace(/</g, '\\u003c')};frame.sandbox='allow-scripts';addEventListener('message',event=>{if(event.source!==frame.contentWindow||event.data?.channel!==channel)return;const {kind,height}=event.data;if(kind==='size'&&Number.isFinite(height)){const h=Math.max(120,Math.min(1200,Math.ceil(height)));frame.style.height=h+'px';parent.postMessage({channel,kind,height:h},'*')}else if(kind==='ready'||kind==='error')parent.postMessage({channel,kind},'*')});frame.srcdoc=${JSON.stringify(child).replace(/</g, '\\u003c')};document.body.append(frame)})()</script></body></html>`
  return { html: wrapper, title }
}

import { useEffect, useState } from 'react'
import { Laptop } from 'lucide-react'
import { useShallow } from 'zustand/react/shallow'
import api from '../../services/api'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { useChatStore } from '../../stores/useChatStore'
import { desktopRequest } from '../../services/desktopConnections'
import { remoteProjectConnection } from '../../utils/remoteSessions'

// Server projects this app already asked to link Downloads for, once per run of the app.
const downloadsAsked = new Set<string>()

export function CodeChatConnectionStatus({ sessionId }: { sessionId: string }) {
  const { project, profile } = useChatStore(useShallow(state => {
    const tab = Object.values(state.chatTabs).find(item => item.sessionId === sessionId)
    return {
      project: tab?.metadata?.agentProfileProjectId,
      profile: tab?.metadata?.agentProfileId,
    }
  }))
  const endpoint = laptopSettingsEndpoint(profile, project)
  const [computer, setComputer] = useState('')
  const [folders, setFolders] = useState<string[]>([])
  const [thisMac, setThisMac] = useState('')
  // A server project opened in the Mac app: say "on this Mac" when the chat uses this Mac (owner, 27 Nov).
  const connection = remoteProjectConnection(project)
  useEffect(() => {
    let active = true
    setThisMac('')
    if (connection) void desktopRequest<{ device_id: string }>(`/${encodeURIComponent(connection)}/this-mac`).then(result => { if (active) setThisMac(result.device_id) }).catch(() => undefined)
    return () => { active = false }
  }, [connection])
  // Opening a server project here links this Mac's Downloads (Read) for the person, like this Mac's own projects
  // (owner, 28 Nov, #4504). The Mac app's backend decides: only when nothing is saved for the project yet.
  useEffect(() => {
    const key = connection && project ? `${connection}/${project}` : ''
    if (!key || downloadsAsked.has(key)) return
    downloadsAsked.add(key)
    void desktopRequest<{ linked: boolean }>(`/${encodeURIComponent(connection!)}/this-mac/downloads?project=${encodeURIComponent(project!)}`, 'POST')
      .then(result => { if (result.linked) window.dispatchEvent(new Event('crew-laptop-settings-changed')) }).catch(() => undefined)
  }, [connection, project])
  useEffect(() => {
    let active = true
    setComputer('')
    const load = () => {
      if (endpoint) void api.get<{ device_id: string; folders: { path: string }[] }>(endpoint, { skipSessionContext: true })
        .then(result => { if (active) { setComputer(result.data.folders?.length ? result.data.device_id : ''); setFolders((result.data.folders || []).map(item => item.path)) } }).catch(() => { if (active) setComputer('') })
    }
    load()
    window.addEventListener('crew-laptop-settings-changed', load)
    return () => { active = false; window.removeEventListener('crew-laptop-settings-changed', load) }
  }, [endpoint])
  if (!computer) return null
  // Opens the Crew's Local laptop panel, the way the model name opens Models.
  const where = computer === thisMac ? 'this Mac' : computer
  const short = folders.map(path => path.replace(/^\/Users\/[^/]+/, '~'))
  // Every linked folder counts, not only the first (#4535): "~/Downloads + 1", all of them on hover.
  const label = short.length ? `Using ${short[0]}${short.length > 1 ? ` + ${short.length - 1}` : ''} on ${where}` : `Laptop · ${where}`
  const title = short.length > 1 ? `Using ${short.join(', ')} on ${where}` : label
  return <button type="button" title={`${title}. Open the laptop settings.`} onClick={() => window.dispatchEvent(new CustomEvent('agentworks:open-chat-laptop', { detail: { projectId: project } }))}
    className="inline-flex h-7 min-w-0 max-w-64 items-center gap-1.5 rounded px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"><Laptop className="h-3.5 w-3.5 shrink-0" /><span className="truncate max-md:sr-only">{label}</span></button>
}

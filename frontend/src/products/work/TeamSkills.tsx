import { useCallback, useEffect, useState } from 'react'
import { Sparkles } from 'lucide-react'
import { shortAgo } from '../../components/chat/AgentIcon'
import { useChatStore } from '../../stores/useChatStore'
import { sendToOpenChat } from '../../utils/agentPurposeDraft'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

/** Skills the Team shares (#3459, owner 20 Nov): optional both ways, like commands, schedules and themes. Sharing puts one
 * skill (its text files: SKILL.md and any scripts) in the Team's shared folder as <folder>/shared-skills/<id>.json. Nothing
 * is installed for anyone: "Add to my skills" and "Share one of my skills" ask the person's own agent, which shows the
 * steps and every script before installing, never overwrites a skill the person has, and keeps key values out (keys are
 * named only; their values stay in the Team vault). The screen only lists and removes. */

type SharedFile = { path: string; hash: string; author?: string; mine?: boolean; modified_at?: string }
type SkillFile = { kind: 'skill'; name: string; description?: string; files: Record<string, string> }
type SharedSkill = SharedFile & { file: SkillFile }
const route = (path: string, hash = '') => `/file?path=${encodeURIComponent(path)}&base=${encodeURIComponent(hash)}&transfer=${encodeURIComponent(crypto.randomUUID())}`

/** The one line SKILL.md gives for itself (its front-matter description), when it has one. */
const describe = (file: SkillFile) => file.description?.trim() || /^description:\s*["']?(.+?)["']?\s*$/m.exec(file.files['SKILL.md'] ?? '')?.[1] || ''
/** Key names a skill mentions (SECRET_NAME); names are shown, values never travel. */
const keysOf = (file: SkillFile) => [...new Set(Object.values(file.files).flatMap(text => text.match(/\bSECRET_[A-Z0-9_]+\b/g) ?? []))].map(key => key.replace(/^SECRET_/, ''))

const toast = () => useChatStore.getState().addToast('Open one of your own agent chats in this Crew, then try again. Nothing was sent.', 'info')

/** `folder` is the Team's first shared folder; no shared folder means nothing to show here. */
export function TeamSkills({ target, projectId, folder }: { target: TeamTarget; projectId: string; folder?: string }) {
  const [shared, setShared] = useState<SharedSkill[]>([])
  const [busy, setBusy] = useState(false)
  const [line, setLine] = useState('')
  const dir = folder === undefined ? '' : `${folder === '.' ? '' : `${folder}/`}shared-skills/`
  const load = useCallback(async () => {
    if (folder === undefined) return
    try {
      const data = await teamRequest<{ files?: SharedFile[] }>(target, '/files')
      const files = (data.files ?? []).filter(file => file.path.startsWith(dir) && file.path.endsWith('.json')).slice(0, 40)
      const read = await Promise.all(files.map(async file => {
        try {
          const parsed = JSON.parse(await teamRequest<string>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, true)) as Partial<SkillFile>
          return parsed.kind === 'skill' && parsed.name?.trim() && parsed.files && typeof parsed.files['SKILL.md'] === 'string' ? { ...file, file: parsed as SkillFile } : null
        } catch { return null }
      }))
      setShared(read.filter((item): item is SharedSkill => !!item))
    } catch { setShared([]) }
  }, [target.project, target.connection, target.folderId, dir]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    void load()
    window.addEventListener('agentworks:team-updated', load)
    return () => window.removeEventListener('agentworks:team-updated', load)
  }, [load])
  if (folder === undefined) return null
  const ask = (message: string) => { if (!sendToOpenChat(projectId, message)) toast() }
  const add = (item: SharedSkill) => ask(`Add the Team's shared skill “${item.file.name}” to my skills. Read it from the Team files (${item.path}), show me its steps and every script in it before you install anything, and ask me first. Put it in my own skills under that name, or another name if I already have one; never overwrite one of mine. It names its keys only; the values stay in the Team vault.`)
  const share = () => ask('Share one of my skills with the Team. Ask me which one first, then follow "Sharing a skill with the Team" in the Team guide.')
  const stop = async (item: SharedSkill) => {
    setBusy(true)
    try { await teamRequest(target, route(item.path, item.hash), 'DELETE'); setLine(`Stopped sharing “${item.file.name}”. Copies teammates already added stay with them.`) } catch (e) { setLine(teamFailure(e).message) } finally { setBusy(false); void load() }
  }
  return <section aria-label="Shared skills" className="space-y-1.5">
    <h4 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Shared skills <span className="font-normal normal-case tracking-normal">· share a skill; teammates add it only if they want it</span></h4>
    {shared.length > 0 && <ul className="space-y-1.5">{shared.map(item => {
      const names = Object.keys(item.file.files)
      const keys = keysOf(item.file)
      return <li key={item.path} className="rounded-xl border border-border bg-muted/20 px-3 py-2">
        <div className="flex items-center gap-3">
          <Sparkles className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="min-w-0 flex-1"><span className="block truncate font-medium">{item.file.name}</span><span className="block text-[11px] text-muted-foreground">{[describe(item.file), item.mine ? 'Shared by you' : item.author && `Shared by ${item.author}`, shortAgo(item.modified_at)].filter(Boolean).join(' · ')}</span></span>
          {!item.mine && <button type="button" onClick={() => add(item)} className="rounded-lg border border-border px-2.5 py-1 text-xs hover:bg-muted" aria-label={`Add ${item.file.name} to my skills`}>Add to my skills</button>}
          {item.mine && <button type="button" disabled={busy} onClick={() => void stop(item)} className="rounded-lg px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50" aria-label={`Stop sharing ${item.file.name}`}>Stop sharing</button>}
        </div>
        <p className="mt-1 break-words rounded border border-border bg-background px-2 py-1 font-mono text-[11px] text-muted-foreground">{names.join(' · ')}{keys.length > 0 && <span className="block">Needs key {keys.join(', ')} (name only; the value stays in the Team vault)</span>}</p>
      </li>
    })}</ul>}
    {!shared.length && <p className="text-xs text-muted-foreground">No shared skills yet. Share one of yours and teammates can add it if they want.</p>}
    <button type="button" onClick={share} className="rounded-lg border border-border px-2.5 py-1 text-xs hover:bg-muted">Share one of my skills</button>
    {line && <p role="status" className="text-xs text-muted-foreground">{line}</p>}
  </section>
}

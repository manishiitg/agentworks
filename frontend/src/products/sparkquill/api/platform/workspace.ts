// The family workspace as the SparkQuill UI sees it, served by the
// platform's workspace API through the agent server's proxy (/api/wp).
// Paths on this side are family-root-relative ("reports/progress.html");
// the proxy stamps the user, and "Chats/…" resolves to that user's tree.
import type { Activity, StoredMsg, TreeNode } from '../../stores/types'
import type { FileContent, TreeResponse, UploadResult } from '../familyApi'

export const FAMILY_ROOT = 'Chats/SparkQuill'
export const ACTIVITIES = 'activities'

export type Requester = <T>(method: string, path: string, body?: unknown) => Promise<T>

// engine (which AI provider) is family-wide by design — it corresponds to
// which paid account the family has (Settings: "The AI behind both your
// chat and {child}'s tutor... pick whichever account you already pay for").
// The MODEL within that engine is not: product.yaml declares a model_id per
// profile so a family can choose different models for parent and child on the
// same account. The parent defaults to GPT-6.1 Sol and the child to GPT-6 Luna. A
// single shared `model` field (now legacy, read-only going forward) let
// whichever side picked last silently overwrite the other's — parent_model/
// child_model are the fix. familyRuntime() falls back to each profile's own
// product.yaml default when its role-specific field has never been set.
export type FamilyFile = { engine?: string; model?: string; parent_model?: string; child_model?: string; connection_id?: string; parent_reasoning_effort?: string; child_reasoning_effort?: string; child?: { name?: string; grade?: string; board?: string } | null; parent_label?: string; pin_hash?: string; watch_sites?: string[] }

export async function sha256Hex(text: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text))
  return Array.from(new Uint8Array(digest)).map((b) => b.toString(16).padStart(2, '0')).join('')
}

type Document = { filepath: string; type?: string; children?: Document[]; size?: number; content?: string; is_binary?: boolean; is_image?: boolean; mime_type?: string }
type APIResponse<T> = { success?: boolean; data?: T; error?: string }

/** Turns a family-relative path into the proxy's user-relative one. */
export function workspacePath(rel: string): string {
  const clean = String(rel ?? '').replace(/^\/+/, '')
  if (clean.startsWith('_users/') || clean === FAMILY_ROOT || clean.startsWith(`${FAMILY_ROOT}/`)) return clean
  return clean ? `${FAMILY_ROOT}/${clean}` : FAMILY_ROOT
}

/** Strips the family root (and any per-user prefix) off a workspace path. */
export function familyRelative(path: string): string {
  const clean = String(path ?? '').replace(/^\/+/, '')
  const marker = `${FAMILY_ROOT}/`
  const i = clean.indexOf(marker)
  if (i >= 0) return clean.slice(i + marker.length)
  return clean === FAMILY_ROOT ? '' : clean
}

export function encodePath(path: string): string {
  return path.split('/').map(encodeURIComponent).join('/')
}

export function documentsURL(rel: string, suffix = ''): string {
  return `/api/wp/api/documents/${encodePath(workspacePath(rel))}${suffix}`
}

export function foldersURL(rel: string): string {
  return `/api/wp/api/folders/${encodePath(workspacePath(rel))}?confirm=true`
}

export class FamilyWorkspace {
  private readonly request: Requester

  constructor(request: Requester) {
    this.request = request
  }

  async readFile(rel: string): Promise<FileContent> {
    const resp = await this.request<APIResponse<Document>>('GET', documentsURL(rel))
    const d: Document = resp?.data ?? { filepath: rel }
    return { path: familyRelative(rel), is_text: !d.is_binary, content: d.content ?? '', size: d.size }
  }

  async readJSON<T>(rel: string): Promise<T | null> {
    const f = await this.readFile(rel).catch(() => null)
    if (!f?.content) return null
    try { return JSON.parse(f.content) as T } catch { return null }
  }

  async writeFile(rel: string, content: string): Promise<void> {
    await this.request('PUT', documentsURL(rel), { content })
  }

  async writeJSON(rel: string, value: unknown): Promise<void> {
    await this.writeFile(rel, JSON.stringify(value, null, 2) + '\n')
  }

  /** Permanently removes a folder and everything under it. No undo. */
  async deleteFolder(rel: string): Promise<void> {
    await this.request('DELETE', foldersURL(rel))
  }

  /** The family tree in the UI's shape: family-relative paths, folders first as the API returns them. */
  async tree(): Promise<TreeResponse> {
    const resp = await this.request<APIResponse<Document[]>>('GET', `/api/wp/api/documents?folder=${encodeURIComponent(FAMILY_ROOT)}&max_depth=-1`)
    let total = 0
    const convert = (docs: Document[] | undefined): TreeNode[] => (docs ?? []).map((d) => {
      const path = familyRelative(d.filepath)
      const node: TreeNode = { name: path.split('/').pop() ?? path, path, type: d.type === 'folder' ? 'dir' : 'file' }
      if (d.type === 'folder') node.children = convert(d.children)
      else { node.size = d.size; total += d.size ?? 0 }
      return node
    })
    const nodes = convert(resp?.data)
    return { nodes, total_size: total }
  }

  async upload(file: File, folderRel: string): Promise<UploadResult> {
    const fd = new FormData()
    fd.append('folder_path', workspacePath(folderRel))
    fd.append('file', file)
    const resp = await this.request<{ filepath?: string; success?: boolean; error?: string; data?: { filepath?: string } }>('POST', '/api/wp/api/upload', fd)
    const saved = resp?.filepath ?? resp?.data?.filepath ?? `${folderRel}/${file.name}`
    if (resp?.error) return { name: file.name, error: resp.error }
    return { name: saved.split('/').pop() ?? file.name, path: familyRelative(saved) }
  }

  /** The iframe scene bridge's save/load, one JSON file per key. */
  stateFile(key: string): string {
    const safe = key.replace(/[^A-Za-z0-9._-]+/g, '_').slice(0, 120) || 'state'
    return `state/${safe}.json`
  }

  async activity(dir: string): Promise<Activity | null> {
    const rel = familyRelative(dir).replace(/\/+$/, '')
    const [m, product, legacy] = await Promise.all([
      this.readJSON<{ title?: string; subject?: string; topic?: string; items?: string[]; goal?: string; persona?: string; created_at?: string }>(`${rel}/activity.json`),
      // product.json makes the folder a keyed conversation (product_conversation_registry.go) — its
      // session_id is the child's conversation with the tutor here, for the parent's read-only view.
      this.readJSON<{ session_id?: string }>(`${rel}/product.json`),
      // Present only for an activity migrated from the old standalone app —
      // its product.json session_id is freshly invented by the migration
      // with nothing behind it in chat_history; this frozen transcript is
      // the real content. Absent (readJSON resolves null on 404) for any
      // activity created on the platform.
      this.readJSON<{ messages?: StoredMsg[] }>(`${rel}/legacy-conversation.json`),
    ])
    if (!m) return null
    return {
      dir: rel,
      title: m.title ?? rel.split('/').pop() ?? rel,
      subject: m.subject,
      topic: m.topic,
      items: (m.items ?? []).map((name) => ({ path: `${rel}/${name}`, name })),
      goal: m.goal,
      persona: m.persona,
      created_at: m.created_at,
      attempts: [],
      session_id: product?.session_id,
      legacy_conversation: legacy,
    }
  }

  async activities(): Promise<Activity[]> {
    const resp = await this.request<APIResponse<Document[]>>('GET', `/api/wp/api/documents?folder=${encodeURIComponent(`${FAMILY_ROOT}/${ACTIVITIES}`)}&max_depth=1`).catch(() => null)
    const folders = (resp?.data ?? []).filter((d) => d.type === 'folder')
    const out = await Promise.all(folders.map((d) => this.activity(d.filepath)))
    return out.filter((a): a is Activity => a !== null).sort((a, b) => (b.created_at ?? '').localeCompare(a.created_at ?? ''))
  }

  async currentActivity(): Promise<Activity | null> {
    const pointer = await this.readJSON<{ dir?: string }>('current-activity.json')
    if (!pointer?.dir) return null
    return this.activity(pointer.dir)
  }

  // ---- family.json: the setup screen's state ------------------------------
  // Same file and same PIN hashing (hex SHA-256 of the digits) the family
  // server used, so a family set up there keeps working here.

  async readFamily(): Promise<FamilyFile> {
    return (await this.readJSON<FamilyFile>('family.json')) ?? {}
  }

  // engine is shared (which paid account the family uses); the model within
  // it is per-role (see FamilyFile's comment) — role picks which of
  // parent_model/child_model this call's model belongs to. The legacy shared
  // `model` field is never written here — see familyRuntime()'s fallback.
  async saveEngine(role: 'parent' | 'child', engine: string, model?: string, connectionId?: string, reasoningEffort?: string): Promise<void> {
    const current = await this.readFamily()
    const modelKey = role === 'parent' ? 'parent_model' : 'child_model'
    // A model belongs to its engine: switching engine without naming a model
    // drops this role's old one rather than carrying a foreign model id along.
    const next: Record<string, unknown> = { ...current, engine: engine.trim() }
    if (current.engine !== engine.trim()) {
      for (const key of ['model', 'parent_model', 'child_model', 'connection_id', 'parent_reasoning_effort', 'child_reasoning_effort']) delete next[key]
    }
    if (connectionId !== undefined) next.connection_id = connectionId.trim()
    if (reasoningEffort !== undefined) next[`${role}_reasoning_effort`] = reasoningEffort
    if (model && model.trim()) next[modelKey] = model.trim()
    else delete next[modelKey]
    await this.writeJSON('family.json', next)
  }

  async saveChild(child: { name: string; grade: string; board: string }): Promise<void> {
    const current = await this.readFamily()
    await this.writeJSON('family.json', { ...current, child: { ...(current.child ?? {}), name: child.name.trim(), grade: child.grade.trim(), board: child.board.trim() } })
  }

  async setPin(pin: string): Promise<{ error?: string }> {
    const digits = pin.trim()
    if (!/^\d{4,8}$/.test(digits)) return { error: 'PIN must be 4–8 digits' }
    const current = await this.readFamily()
    await this.writeJSON('family.json', { ...current, pin_hash: await sha256Hex(digits) })
    return {}
  }

  /** True when the PIN matches, or when no PIN has been set yet. */
  async verifyPin(pin: string): Promise<{ ok: boolean }> {
    const current = await this.readFamily()
    if (!current.pin_hash) return { ok: true }
    return { ok: (await sha256Hex(pin.trim())) === current.pin_hash }
  }

  /**
   * "Give to <child>": binds the child to one activity by moving the
   * current-activity pointer, the same file the family server kept. Returns
   * what the app needs to enter child mode: the activity and whether the
   * child starts a fresh conversation. `resume` (when given) decides that
   * outright; otherwise a different activity than the current one is fresh.
   */
  async handoff(dir: string, resume?: boolean): Promise<{ dir: string; title: string; goal?: string; new_session: boolean }> {
    const act = await this.activity(dir)
    if (!act) throw new Error('activity not found')
    const pointer = await this.readJSON<{ dir?: string }>('current-activity.json')
    const previous = familyRelative(pointer?.dir ?? '').replace(/\/+$/, '')
    const newSession = resume === undefined ? act.dir !== previous : !resume
    await this.writeJSON('current-activity.json', { dir: act.dir })
    return { dir: act.dir, title: act.title, goal: act.goal, new_session: newSession }
  }

}

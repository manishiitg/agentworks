import { createContext, useContext, useEffect, useState, Fragment, type ComponentPropsWithoutRef } from 'react'
import type { Components } from 'react-markdown'
import api from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import type { CodeRef } from '../../utils/projectFileLinks'

export type ChatFileTarget = { path: string; preview?: boolean }
type Lookup = (path: string) => Promise<ChatFileTarget | undefined>

export const ChatFilePaths = createContext<{
  lookup: Lookup
  open: (ref: CodeRef) => void
} | null>(null)

export async function resolveChatFilePaths(workspace: string, connection: string | undefined, identity: number, paths: string[], signal?: AbortSignal) {
  const result = await api.post<Record<string, ChatFileTarget>>('/api/chat-file-paths', {
    workspace_path: workspace, paths,
  }, { connection, skipSessionContext: true, chatIdentityGeneration: identity, signal })
  return result.data
}

/** Coalesce references from mounted transcript rows, never on every stream token.
 * One request at a time, with scope and a small cache owned by this Crew view. */
export function createChatFileLookup(workspace: string, connection?: string): Lookup {
  const identity = captureChatIdentity()
  const cache = new Map<string, { result: Promise<ChatFileTarget | undefined>; until: number }>()
  const pending = new Map<string, (target?: ChatFileTarget) => void>()
  let scheduled = false
  async function flush() {
    const paths = Array.from(pending.keys()).slice(0, 24)
    const targets = isChatIdentityCurrent(identity)
      ? await resolveChatFilePaths(workspace, connection, identity, paths).catch(() => null)
      : null
    for (const path of paths) {
      const resolve = pending.get(path)
      pending.delete(path)
      const target = isChatIdentityCurrent(identity) ? targets?.[path] : undefined
      const item = cache.get(path)
      if (item) item.until = Date.now() + (target ? 60_000 : 10_000)
      resolve?.(target?.path ? target : undefined)
    }
    if (pending.size) window.setTimeout(() => void flush(), 25)
    else scheduled = false
  }
  return path => {
    if (!isChatIdentityCurrent(identity)) return Promise.resolve(undefined)
    const prior = cache.get(path)
    if (prior && prior.until > Date.now()) return prior.result
    if (pending.size >= 256) return Promise.resolve(undefined)
    const result = new Promise<ChatFileTarget | undefined>(resolve => pending.set(path, resolve))
    cache.set(path, { result, until: Infinity })
    if (cache.size > 256) {
      for (const key of cache.keys()) {
        if (!pending.has(key)) cache.delete(key)
        if (cache.size <= 256) break
      }
    }
    if (!scheduled) {
      scheduled = true
      window.setTimeout(() => void flush(), 25)
    }
    return result
  }
}

// File-like names only in prose; no URL fragments, shell expansion or guessing
// boundaries for spaces. Wrap paths with spaces in backticks (or a link).
const FILE = /^(.*\.[A-Za-z][A-Za-z0-9]{0,15})(?::([1-9]\d{0,6})(?::[1-9]\d{0,4})?)?$/
const PROSE_FILE = /(?<![^\s([{"'“‘])(?:\.\/|\/)?(?:[\w.@+-]+\/)*[\w@.+-]+\.[A-Za-z][A-Za-z0-9]{0,15}(?::[1-9]\d{0,6}(?::[1-9]\d{0,4})?)?(?=$|[\s)\]}"'”’,;!?]|\.(?!\w))/g

export function parseChatFilePath(value: string): CodeRef | null {
  const match = FILE.exec(value)
  if (!match || value.length > 2048 || /[\\<>?#%\u0000-\u001f]/.test(value)) return null
  const file = match[1].replace(/^\.\//, '')
  const segments = file.replace(/^\//, '').split('/')
  if (segments.some(part => !part || part.trim() !== part || part === '.' || part === '..' || part.includes(':'))) return null
  return { path: file, ...(match[2] ? { line: Number(match[2]) } : {}) }
}

type PathNode = {
  type: string
  value?: string
  children?: PathNode[]
  data?: { hName: string; hProperties: Record<string, string | number> }
  url?: string
}
const reference = (ref: CodeRef, children: PathNode[]): PathNode => ({
  type: 'chatFilePath',
  children,
  data: { hName: 'span', hProperties: { dataChatFilePath: ref.path, dataChatFileLine: ref.line || 0 } },
})

/** Operate on Markdown nodes so fenced code, images and URLs are untouched. */
export function remarkChatFilePaths() {
  return function visit(node: PathNode): void {
    if (!node.children) return
    node.children = node.children.flatMap(child => {
      if (child.type === 'inlineCode' || child.type === 'link') {
        let path = (child.type === 'link' ? child.url : child.value) ?? ''
        if (child.type === 'link') {
          try { path = decodeURI(path) } catch { return [child] }
        }
        const ref = parseChatFilePath(path)
        return ref ? [reference(ref, child.type === 'link' ? child.children ?? [] : [child])] : [child]
      }
      if (child.type === 'text' && child.value) {
        return pathTextPieces(child.value).map(({ text, ref }) => {
          const node = { type: 'text', value: text }
          return ref ? reference(ref, [node]) : node
        })
      }
      if (child.type !== 'linkReference' && child.type !== 'image' && child.type !== 'imageReference') visit(child)
      return [child]
    })
  }
}

function pathTextPieces(value: string, literalMessage = false): { text: string; ref?: CodeRef }[] {
  const pieces: { text: string; ref?: CodeRef }[] = []
  // User messages keep their literal presentation. Recognize backticked
  // paths there, but don't turn a fenced example into a list of links.
  const pattern = literalMessage
    ? new RegExp('`{3}[\\s\\S]*?(?:`{3}|$)|`([^`\\r\\n]+)`|' + PROSE_FILE.source, 'g')
    : PROSE_FILE
  let end = 0
  for (const match of value.matchAll(pattern)) {
    const offset = match.index ?? 0
    if (offset > end) pieces.push({ text: value.slice(end, offset) })
    const ref = match[0].startsWith('```') ? null : parseChatFilePath(match[1] ?? match[0])
    pieces.push({ text: match[0], ref: ref ?? undefined })
    end = offset + match[0].length
  }
  if (end < value.length) pieces.push({ text: value.slice(end) })
  return pieces
}

export function ChatFilePathText({ text }: { text: string }) {
  const scope = useContext(ChatFilePaths)
  if (!scope) return text
  return pathTextPieces(text, true).map(({ text, ref }, index) =>
    ref ? <ChatFileReference key={index} path={ref.path} line={ref.line}>{text}</ChatFileReference> : <Fragment key={index}>{text}</Fragment>)
}

export const ChatFilePathSpan: NonNullable<Components['span']> = ({ node, children, ...props }) => {
  const path = node?.properties.dataChatFilePath
  if (typeof path !== 'string') return <span {...props}>{children}</span>
  return <ChatFileReference path={path} line={Number(node?.properties.dataChatFileLine) || undefined}>{children}</ChatFileReference>
}

function ChatFileReference({ path, line, children }: ComponentPropsWithoutRef<'span'> & { path: string; line?: number }) {
  const scope = useContext(ChatFilePaths)
  const [resolved, setResolved] = useState<{ scope: typeof scope; path: string }>()
  useEffect(() => {
    if (!scope) return
    let current = true
    void scope.lookup(path).then(target => { if (current && target) setResolved({ scope, path }) })
    return () => { current = false }
  }, [scope, path])
  if (!scope || resolved?.scope !== scope || resolved.path !== path) return <span>{children}</span>
  return <a href="#" className="text-primary underline underline-offset-2 break-words" title={`Open ${path}${line ? ` at line ${line}` : ''}`} onClick={event => {
    event.preventDefault()
    event.stopPropagation()
    scope.open({ path, line })
  }}>{children}</a>
}

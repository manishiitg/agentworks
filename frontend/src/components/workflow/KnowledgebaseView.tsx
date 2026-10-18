// Knowledgebase popup — read-only viewer for the workspace KB:
// notes/{topic}.md + notes/_index.json (per-topic narrative). Reads via the
// existing workspace file API (same path as ReportViewer). All mutations happen
// via the workshop builder's reorganize_knowledgebase tool — this popup never writes.

import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  ArrowLeft,
  Database,
  Loader2,
  AlertCircle,
  ChevronDown,
  ChevronRight,
  FileText,
  Search,
} from 'lucide-react'
import { agentApi, workflowManifestApi } from '../../services/api'
import { KnowledgebaseSources } from './KnowledgebaseSources'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'

interface KnowledgebaseViewProps {
  workspacePath: string | null
  headerAction?: ReactNode
  /** Embedded in the Knowledge umbrella: the umbrella owns the header. */
  hideHeader?: boolean
}

interface KBNotesTopic {
  id: string
  file: string
  covers?: string[]
  last_updated?: string
  last_updated_by?: { step?: string; run?: string }
  size_bytes?: number
  section_count?: number
}

interface KBNotesIndex {
  topics?: KBNotesTopic[]
  last_updated?: string
  last_updated_by?: { step?: string; run?: string }
}

type KBFileFreshness = {
  lastConfirmedAt: string
  lastAction: string
}

type LegacyKBTopicMeta = {
  covers?: unknown
  last_updated?: unknown
  last_updated_by?: unknown
  size_bytes?: unknown
  section_count?: unknown
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

async function readJSON<T>(filepath: string): Promise<T | null> {
  try {
    const resp = await agentApi.getPlannerFileContent(filepath)
    if (!resp?.success || !resp.data?.content) return null
    return JSON.parse(resp.data.content) as T
  } catch {
    return null
  }
}

async function readText(filepath: string): Promise<string | null> {
  try {
    const resp = await agentApi.getPlannerFileContent(filepath)
    if (!resp?.success || typeof resp.data?.content !== 'string') return null
    return resp.data.content
  } catch {
    return null
  }
}

function topicIdFromFile(file: string): string {
  return file.replace(/\.md$/i, '')
}

function normalizeTopicFilePath(value: unknown, topicId: string): string {
  let file = stringValue(value).replace(/^\/+/, '')
  const notesPrefix = 'knowledgebase/notes/'
  const shortNotesPrefix = 'notes/'
  if (file.startsWith(notesPrefix)) file = file.slice(notesPrefix.length)
  if (file.startsWith(shortNotesPrefix)) file = file.slice(shortNotesPrefix.length)
  return file || (topicId ? `${topicId}.md` : '')
}

function normalizeUpdatedBy(value: unknown): KBNotesTopic['last_updated_by'] {
  if (!value) return undefined
  if (typeof value === 'string') return { step: value }
  if (typeof value !== 'object') return undefined
  const record = value as Record<string, unknown>
  return {
    step: typeof record.step === 'string' ? record.step : undefined,
    run: typeof record.run === 'string' ? record.run : undefined,
  }
}

function normalizeTopic(value: unknown, fallbackFile?: string): KBNotesTopic | null {
  if (!value || typeof value !== 'object') return null
  const record = value as Record<string, unknown>
  const topicId = stringValue(record.id) || stringValue(record.topic_id) || stringValue(record.topic)
  const file = normalizeTopicFilePath(record.file ?? record.file_path ?? record.path ?? fallbackFile, topicId)
  const id = topicId || topicIdFromFile(file)
  if (!id || !file) return null
  return {
    id,
    file,
    covers: Array.isArray(record.covers)
      ? record.covers.filter((item): item is string => typeof item === 'string')
      : undefined,
    last_updated: typeof record.last_updated === 'string' ? record.last_updated : undefined,
    last_updated_by: normalizeUpdatedBy(record.last_updated_by),
    size_bytes: typeof record.size_bytes === 'number' ? record.size_bytes : undefined,
    section_count: typeof record.section_count === 'number' ? record.section_count : undefined,
  }
}

function normalizeKBIndex(raw: unknown): KBNotesIndex | null {
  if (!raw || typeof raw !== 'object') return null
  const record = raw as Record<string, unknown>

  // Existing shape: { topics: [{ id, file, ... }] }
  if (Array.isArray(record.topics)) {
    const topics = record.topics
      .map((topic) => normalizeTopic(topic))
      .filter((topic): topic is KBNotesTopic => topic !== null)
      .sort((a, b) => a.id.localeCompare(b.id))
    return {
      topics,
      last_updated: typeof record.last_updated === 'string' ? record.last_updated : undefined,
      last_updated_by: normalizeUpdatedBy(record.last_updated_by),
    }
  }

  // Current shape: { notes: [{ topic_id, file_path, ... }] }
  if (Array.isArray(record.notes)) {
    const topics = record.notes
      .map((note) => normalizeTopic(note))
      .filter((topic): topic is KBNotesTopic => topic !== null)
      .sort((a, b) => a.id.localeCompare(b.id))
    return {
      topics,
      last_updated: typeof record.last_updated === 'string' ? record.last_updated : undefined,
      last_updated_by: normalizeUpdatedBy(record.last_updated_by),
    }
  }

  // Legacy flat shape: { "<file>.md": { ...meta } }
  const topics = Object.entries(record)
    .filter(([file, meta]) => file.toLowerCase().endsWith('.md') && meta && typeof meta === 'object' && !Array.isArray(meta))
    .map(([file, rawMeta]) => {
      const meta = rawMeta as LegacyKBTopicMeta
      return normalizeTopic({ ...meta, file }, file)
    })
    .filter((topic): topic is KBNotesTopic => topic !== null)
    .sort((a, b) => a.id.localeCompare(b.id))

  return { topics }
}

function parseKBFileFreshness(raw: unknown): Record<string, KBFileFreshness> {
  if (!raw || typeof raw !== 'object') return {}
  const items = (raw as { items?: unknown }).items
  if (!items || typeof items !== 'object') return {}

  return Object.fromEntries(
    Object.entries(items as Record<string, unknown>).flatMap(([path, value]) => {
      if (!value || typeof value !== 'object') return []
      const entry = value as { last_confirmed_at?: unknown; last_action?: unknown }
      const lastConfirmedAt = typeof entry.last_confirmed_at === 'string' ? entry.last_confirmed_at : ''
      if (!lastConfirmedAt) return []
      return [[path, {
        lastConfirmedAt,
        lastAction: typeof entry.last_action === 'string' ? entry.last_action : '',
      }]]
    }),
  )
}

function formatFreshnessDate(timestamp: string): string {
  const date = new Date(timestamp)
  if (!Number.isFinite(date.getTime())) return 'Fresh'
  return `Fresh ${date.toLocaleDateString([], { month: 'short', day: 'numeric' })}`
}

export default function KnowledgebaseView({ workspacePath, headerAction, hideHeader = false }: KnowledgebaseViewProps) {
 const [sourceAlias, setSourceAlias] = useState('')
 useEffect(() => setSourceAlias(''), [workspacePath])
 return <div className="flex h-full min-h-0 flex-col">
   <div className="min-h-0 flex-1"><KnowledgebaseContent key={`${workspacePath}:${sourceAlias}`} workspacePath={workspacePath} headerAction={sourceAlias ? undefined : headerAction} hideHeader={hideHeader} sourceAlias={sourceAlias} onSelectSource={setSourceAlias} /></div>
 </div>
}

function KnowledgebaseContent({ workspacePath, headerAction, hideHeader = false, sourceAlias, onSelectSource }: KnowledgebaseViewProps & {sourceAlias: string; onSelectSource: (alias: string) => void}) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notesIndex, setNotesIndex] = useState<KBNotesIndex | null>(null)
  // Per-topic markdown body cache. undefined = not loaded; null = loaded and missing/empty.
  const [notesBodies, setNotesBodies] = useState<Record<string, string | null>>({})
  const [expandedNotes, setExpandedNotes] = useState<Set<string>>(new Set())
  const [searchTerm, setSearchTerm] = useState('')
  const [fileFreshness, setFileFreshness] = useState<Record<string, KBFileFreshness>>({})

  const notesIndexPath = workspacePath
    ? `${workspacePath}/knowledgebase/notes/_index.json`
    : null
  const freshnessPath = workspacePath
    ? `${workspacePath}/knowledgebase/_freshness.json`
    : null

  const readSourceText = useCallback(async (path: string) => {
    if (!sourceAlias || !workspacePath) return readText(path)
    const prefix = `${workspacePath}/knowledgebase/`
    if (!path.startsWith(prefix)) throw new Error('Invalid knowledge path')
    const result = await workflowManifestApi.readKnowledgebaseSource(workspacePath, sourceAlias, path.slice(prefix.length))
    if (!result.success) throw new Error('Shared knowledge could not be read')
    return result.content
  }, [sourceAlias, workspacePath])

  const load = useCallback(async () => {
    if (!notesIndexPath || !freshnessPath) return
    setLoading(true)
    setError(null)
    try {
      const [rawIndex, rawFreshness] = await Promise.all([
        sourceAlias ? readSourceText(notesIndexPath).then(raw => raw === null ? null : JSON.parse(raw)) : readJSON<unknown>(notesIndexPath),
        sourceAlias ? readSourceText(freshnessPath).then(raw => raw === null ? null : JSON.parse(raw)).catch(() => null) : readJSON<unknown>(freshnessPath),
      ])
      setNotesIndex(normalizeKBIndex(rawIndex))
      setFileFreshness(parseKBFileFreshness(rawFreshness))
      // Reset per-topic markdown cache on reload — sizes/content may have changed.
      setNotesBodies({})
      setExpandedNotes(new Set())
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [freshnessPath, notesIndexPath, sourceAlias, readSourceText])

  useEffect(() => {
    load()
  }, [load])

  const toggleNoteExpanded = useCallback(
    async (topic: KBNotesTopic) => {
      if (expandedNotes.has(topic.id)) {
        setExpandedNotes(new Set())
        return
      }
      setExpandedNotes(new Set([topic.id]))
      // Selective load — only fetch the markdown when the row is expanded.
      // Matches the index-first read discipline the KB agent enforces.
      if (notesBodies[topic.id] === undefined && workspacePath) {
        try {
          const body = await readSourceText(`${workspacePath}/knowledgebase/notes/${topic.file}`)
          setNotesBodies(prev => ({ ...prev, [topic.id]: body }))
        } catch (e) { setError(e instanceof Error ? e.message : 'Knowledge file unavailable') }
      }
    },
    [expandedNotes, notesBodies, workspacePath, readSourceText],
  )

  const topics = useMemo(() => notesIndex?.topics ?? [], [notesIndex])
  const hasNotesContent = topics.length > 0
  const focusedTopicId = expandedNotes.values().next().value as string | undefined
  const filteredTopics = useMemo(() => {
    const query = searchTerm.trim().toLowerCase()
    if (!query) return topics
    return topics.filter(topic => [topic.id, topic.file, ...(topic.covers || [])]
      .some(value => value.toLowerCase().includes(query)))
  }, [searchTerm, topics])
  const visibleTopics = focusedTopicId
    ? topics.filter(topic => topic.id === focusedTopicId)
    : filteredTopics

  return (
      <div className="flex h-full min-h-0 w-full flex-col bg-background">
        {/* Header — always first; the source picker below scopes the content. */}
        {!hideHeader && (
          <WorkspaceViewHeader
            icon={Database}
            title="Knowledgebase"
            context={<span className="text-xs text-muted-foreground">notes/ · narrative topics</span>}
            actions={<>
              {headerAction}
              <WorkspaceViewIconButton label="Refresh knowledgebase" onClick={load} disabled={loading} spinning={loading} />
            </>}
          />
        )}

        {/* Source picker — which knowledge this view shows. */}
        {workspacePath && <KnowledgebaseSources workspacePath={workspacePath} selected={sourceAlias} onSelect={onSelectSource} />}

        {/* Summary strip */}
        <div className="flex items-center gap-4 px-4 py-3 border-b border-border flex-shrink-0 text-sm">
          <div>
            <span className="text-muted-foreground">Notes topics: </span>
            <span className="font-medium">{topics.length}</span>
          </div>
          {notesIndex?.last_updated && (
            <div className="text-xs text-muted-foreground ml-auto">
              Last updated: {new Date(notesIndex.last_updated).toLocaleString()}
              {notesIndex.last_updated_by?.step ? ` · ${notesIndex.last_updated_by.step}` : ''}
              {notesIndex.last_updated_by?.run ? ` / ${notesIndex.last_updated_by.run}` : ''}
            </div>
          )}
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-4">
          {loading && !notesIndex && (
            <div className="flex items-center gap-2 text-muted-foreground">
              <Loader2 className="w-4 h-4 animate-spin" />
              Loading knowledgebase…
            </div>
          )}

          {error && (
            <div className="flex items-start gap-2 p-3 rounded-md bg-destructive/10 text-destructive">
              <AlertCircle className="w-4 h-4 flex-shrink-0 mt-0.5" />
              <div className="text-sm">{error}</div>
            </div>
          )}

          {!loading && !error && !hasNotesContent && (
            <div className="text-center py-12 text-muted-foreground">
              <Database className="w-10 h-10 mx-auto opacity-30 mb-3" />
              <div className="text-sm">
                Notes appear here when a step is asked to write them. Ask the Builder to add
                a notes task to a step.
              </div>
            </div>
          )}

          {!loading && !error && hasNotesContent && (
            <div className="space-y-1">
              {focusedTopicId ? (
                <button
                  type="button"
                  onClick={() => setExpandedNotes(new Set())}
                  className="mb-3 inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-sm font-medium shadow-sm transition-colors hover:bg-accent"
                >
                  <ArrowLeft className="h-4 w-4" />
                  Back to all topics
                </button>
              ) : (
                <div className="relative mb-3">
                  <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                  <input
                    value={searchTerm}
                    onChange={event => setSearchTerm(event.target.value)}
                    placeholder="Search topics and coverage"
                    aria-label="Search knowledgebase topics and coverage"
                    className="h-9 w-full rounded-md border border-input bg-background pl-9 pr-3 text-sm outline-none transition-colors focus:border-primary"
                  />
                </div>
              )}
              {visibleTopics.map(t => {
                const isOpenRow = expandedNotes.has(t.id)
                const body = notesBodies[t.id]
                const isMarkdownFile = t.file.toLowerCase().endsWith('.md')
                const freshness = fileFreshness[t.file]
                return (
                  <div key={t.id} className="border border-border rounded-md">
                    <button
                      onClick={() => toggleNoteExpanded(t)}
                      aria-expanded={isOpenRow}
                      aria-label={`${isOpenRow ? 'Close' : 'Open'} ${t.id} knowledgebase topic`}
                      className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/50 transition-colors"
                    >
                      {isOpenRow ? (
                        <ChevronDown className="w-4 h-4 flex-shrink-0" />
                      ) : (
                        <ChevronRight className="w-4 h-4 flex-shrink-0" />
                      )}
                      <FileText className="w-3.5 h-3.5 flex-shrink-0 text-muted-foreground" />
                      <span className="font-medium text-sm">{t.id}</span>
                      <span className="text-xs text-muted-foreground ml-auto font-mono">
                        {t.file}
                      </span>
                      {freshness && (
                        <span
                          className="shrink-0 rounded bg-emerald-500/10 px-1.5 py-0.5 text-[9px] font-medium text-emerald-700 dark:text-emerald-300"
                          title={`Last ${freshness.lastAction || 'confirmed'}: ${new Date(freshness.lastConfirmedAt).toLocaleString()}`}
                        >
                          {formatFreshnessDate(freshness.lastConfirmedAt)}
                        </span>
                      )}
                      {typeof t.section_count === 'number' && (
                        <span className="text-xs text-muted-foreground ml-2">
                          {t.section_count} section{t.section_count === 1 ? '' : 's'}
                        </span>
                      )}
                      {typeof t.size_bytes === 'number' && (
                        <span className="text-xs text-muted-foreground ml-2">
                          {(t.size_bytes / 1024).toFixed(1)}KB
                        </span>
                      )}
                    </button>
                    {isOpenRow && (
                      <div className="border-t border-border px-3 py-2 text-xs space-y-2 bg-muted/20">
                        {Array.isArray(t.covers) && t.covers.length > 0 && (
                          <div>
                            <span className="font-medium">Covers: </span>
                            <span className="font-mono">{t.covers.join(', ')}</span>
                          </div>
                        )}
                        {(t.last_updated_by?.step || t.last_updated_by?.run) && (
                          <div>
                            <span className="font-medium">Last updated by: </span>
                            <span className="font-mono">
                              {[t.last_updated_by?.step, t.last_updated_by?.run].filter(Boolean).join(' / ')}
                            </span>
                          </div>
                        )}
                        {t.last_updated && (
                          <div className="text-muted-foreground">
                            updated {new Date(t.last_updated).toLocaleString()}
                          </div>
                        )}
                        <div>
                          <div className="font-medium mb-1">Content</div>
                          {body === undefined ? (
                            <div className="flex items-center gap-2 text-muted-foreground">
                              <Loader2 className="w-3 h-3 animate-spin" />
                              Loading {t.file}…
                            </div>
                          ) : body === null ? (
                            <div className="text-muted-foreground italic">
                              Topic file missing or empty.
                            </div>
                          ) : isMarkdownFile ? (
                            <div className="text-sm text-foreground bg-background border border-border/60 rounded p-3">
                              <MarkdownRenderer
                                content={body}
                                className="max-w-none !text-sm [&_p]:!text-sm [&_li]:!text-sm [&_code]:!text-[12px]"
                              />
                            </div>
                          ) : (
                            <pre className="whitespace-pre-wrap break-words text-foreground bg-background border border-border/60 rounded p-2 max-h-[60vh] overflow-y-auto">
                              {body}
                            </pre>
                          )}
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
              {!focusedTopicId && visibleTopics.length === 0 && (
                <div className="py-10 text-center text-sm text-muted-foreground">No matching knowledgebase topics.</div>
              )}
            </div>
          )}
        </div>
      </div>
  )
}

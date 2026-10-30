// Database popup: read-only viewer for the workflow's db/db.sqlite tables.
// Report widgets query these tables via SQL, so this gives users a direct way
// to inspect the data behind the live report without opening the file tree.

import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  AlertCircle,
  FileJson,
  Link2,
  Loader2,
  Maximize2,
  Search,
  Table2,
  X,
} from 'lucide-react'
import { agentApi } from '../../services/api'
import type { PlannerFile } from '../../services/api-types'
import { describeDatabaseLoadFailure } from './databaseLoadError'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { WorkspacePanelBack } from '../workspace/WorkspacePanelNavigation'

interface DatabaseViewProps {
  workspacePath: string | null
  /** Embedded in the Knowledge umbrella: the umbrella owns the header. */
  hideHeader?: boolean
}

type FileSummary = {
  kind: 'array' | 'object' | 'jsonl' | 'scalar' | 'text'
  label: string
  detail?: string
}

type ContentCacheEntry = {
  content: string | null
  formatted: string | null
  summary: FileSummary
}

type TablePreview = {
  id: string
  label: string
  totalRows: number
  columns: string[]
  rows: Array<Record<string, unknown>>
}

type DBTable = {
  id: string
  label: string
  rowCount: number
  fields: string[]
  valuesByField: Map<string, Set<string>>
}

type DBRelationship = {
  fromTable: string
  fromField: string
  toTable: string
  toField: string
  matches: number
  total: number
  confidence: 'strong' | 'possible'
}

type MaximizedCell = {
  table: string
  row: number
  column: string
  preview: string
  detail: string
}

function fileName(path: string): string {
  return path.split('/').filter(Boolean).pop() || path
}

function relativeDBPath(workspacePath: string, path: string): string {
  // Table pseudo-paths look like `db/db.sqlite#<table>` — show the table name.
  const hash = path.indexOf('#')
  if (hash >= 0) return path.slice(hash + 1)
  const prefix = `${workspacePath}/db/`
  return path.startsWith(prefix) ? path.slice(prefix.length) : fileName(path)
}

function formatBytes(raw?: number): string | null {
  if (typeof raw !== 'number' || !Number.isFinite(raw) || raw < 0) return null
  if (raw < 1024) return `${raw}B`
  if (raw < 1024 * 1024) return `${(raw / 1024).toFixed(1)}KB`
  return `${(raw / (1024 * 1024)).toFixed(1)}MB`
}

function plannerFileSize(file: PlannerFile): number | undefined {
  const size = (file as PlannerFile & { size?: unknown }).size
  return typeof size === 'number' ? size : undefined
}

function summarizeParsedJSON(value: unknown): FileSummary {
  if (Array.isArray(value)) {
    const first = value.find(row => row && typeof row === 'object' && !Array.isArray(row))
    const columns = first ? Object.keys(first as Record<string, unknown>) : []
    return {
      kind: 'array',
      label: `${value.length} row${value.length === 1 ? '' : 's'}`,
      detail: columns.length > 0 ? `${columns.length} field${columns.length === 1 ? '' : 's'}: ${columns.slice(0, 6).join(', ')}${columns.length > 6 ? ', ...' : ''}` : undefined,
    }
  }
  if (value && typeof value === 'object') {
    const keys = Object.keys(value as Record<string, unknown>)
    return {
      kind: 'object',
      label: `${keys.length} key${keys.length === 1 ? '' : 's'}`,
      detail: keys.length > 0 ? keys.slice(0, 8).join(', ') + (keys.length > 8 ? ', ...' : '') : undefined,
    }
  }
  return { kind: 'scalar', label: value == null ? 'null' : typeof value }
}

function summarizeJSONL(content: string): FileSummary {
  const lines = content.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
  let valid = 0
  let firstKeys: string[] = []
  for (const line of lines) {
    try {
      const parsed = JSON.parse(line)
      valid += 1
      if (firstKeys.length === 0 && parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        firstKeys = Object.keys(parsed)
      }
    } catch {
      // Keep scanning so partially-written logs still show useful counts.
    }
  }
  return {
    kind: 'jsonl',
    label: `${lines.length} line${lines.length === 1 ? '' : 's'}`,
    detail: valid === lines.length
      ? firstKeys.length > 0 ? `${firstKeys.length} field${firstKeys.length === 1 ? '' : 's'}: ${firstKeys.slice(0, 6).join(', ')}${firstKeys.length > 6 ? ', ...' : ''}` : 'valid JSONL'
      : `${valid}/${lines.length} valid JSON line${valid === 1 ? '' : 's'}`,
  }
}

function buildContentCacheEntry(path: string, content: string | null): ContentCacheEntry {
  if (content == null) {
    return { content, formatted: null, summary: { kind: 'text', label: 'missing or empty' } }
  }
  if (path.toLowerCase().endsWith('.jsonl')) {
    return { content, formatted: content, summary: summarizeJSONL(content) }
  }
  try {
    const parsed = JSON.parse(content)
    return {
      content,
      formatted: JSON.stringify(parsed, null, 2),
      summary: summarizeParsedJSON(parsed),
    }
  } catch {
    return { content, formatted: content, summary: { kind: 'text', label: 'text', detail: 'not valid JSON' } }
  }
}

function primitiveKey(value: unknown): string | null {
  if (value == null) return null
  if (typeof value === 'string') return value.trim() === '' ? null : value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return null
}

function tableNameFromLabel(label: string): string {
  const base = label.split('/').pop() || label
  return base.replace(/\.(json|jsonl)$/i, '').replace(/[^a-zA-Z0-9]+/g, '_').replace(/^_|_$/g, '').toLowerCase()
}

function singular(value: string): string {
  if (value.endsWith('ies')) return `${value.slice(0, -3)}y`
  if (value.endsWith('s') && value.length > 1) return value.slice(0, -1)
  return value
}

function createTable(id: string, label: string, rows: Array<Record<string, unknown>>): DBTable | null {
  if (rows.length === 0) return null
  const fields = new Set<string>()
  const valuesByField = new Map<string, Set<string>>()
  for (const row of rows.slice(0, 1000)) {
    for (const [field, rawValue] of Object.entries(row)) {
      fields.add(field)
      const key = primitiveKey(rawValue)
      if (key == null) continue
      const values = valuesByField.get(field) ?? new Set<string>()
      if (values.size < 2000) values.add(key)
      valuesByField.set(field, values)
    }
  }
  return { id, label, rowCount: rows.length, fields: Array.from(fields).sort(), valuesByField }
}

function objectRows(value: unknown): Array<Record<string, unknown>> {
  if (!Array.isArray(value)) return []
  return value.filter((row): row is Record<string, unknown> => Boolean(row) && typeof row === 'object' && !Array.isArray(row))
}

function rowsFromJSONL(content: string): Array<Record<string, unknown>> {
  return content
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .flatMap(line => {
      try {
        const parsed = JSON.parse(line)
        return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? [parsed as Record<string, unknown>] : []
      } catch {
        return []
      }
    })
}

function collectColumns(rows: Array<Record<string, unknown>>, limit = 12): string[] {
  const fields = new Set<string>()
  for (const row of rows.slice(0, 100)) {
    for (const key of Object.keys(row)) {
      if (fields.size < limit) fields.add(key)
    }
  }
  return Array.from(fields)
}

function buildTablePreviewsFromContent(workspacePath: string | null, path: string, content: string | null): TablePreview[] {
  if (!content) return []
  const label = workspacePath ? relativeDBPath(workspacePath, path) : fileName(path)
  if (path.toLowerCase().endsWith('.jsonl')) {
    const rows = rowsFromJSONL(content)
    return rows.length > 0
      ? [{ id: path, label, totalRows: rows.length, columns: collectColumns(rows), rows: rows.slice(0, 25) }]
      : []
  }

  try {
    const parsed = JSON.parse(content)
    const topRows = objectRows(parsed)
    if (topRows.length > 0) {
      return [{ id: path, label, totalRows: topRows.length, columns: collectColumns(topRows), rows: topRows.slice(0, 25) }]
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return []
    const out: TablePreview[] = []
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      const rows = objectRows(value)
      if (rows.length > 0) {
        out.push({
          id: `${path}#${key}`,
          label: `${label}.${key}`,
          totalRows: rows.length,
          columns: collectColumns(rows),
          rows: rows.slice(0, 25),
        })
      }
    }
    return out
  } catch {
    return []
  }
}

function formatCell(value: unknown): string {
  if (value == null) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

function formatCellDetail(value: unknown): string {
  if (value == null) return 'null'
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function canMaximizeCell(value: unknown, text: string): boolean {
  return text.length > 80 || text.includes('\n') || (value != null && typeof value === 'object')
}

function relationshipConfidence(from: DBTable, fromField: string, to: DBTable, toField: string): 'strong' | 'possible' | null {
  const sourceField = fromField.toLowerCase()
  const targetField = toField.toLowerCase()
  const targetName = tableNameFromLabel(to.label)
  const targetSingular = singular(targetName)
  if (targetField === 'id' && sourceField.endsWith('_id')) return 'strong'
  if (targetField === 'id' && (sourceField === `${targetName}_id` || sourceField === `${targetSingular}_id`)) return 'strong'
  if (
    sourceField === targetField &&
    sourceField !== 'id' &&
    /(id|key|name|group|run|symbol|email)/.test(sourceField)
  ) {
    return 'possible'
  }
  return null
}

function inferDBRelationships(tables: DBTable[]): DBRelationship[] {
  const relationships: DBRelationship[] = []
  for (const from of tables) {
    for (const to of tables) {
      if (from.id === to.id) continue
      for (const fromField of from.fields) {
        const fromValues = from.valuesByField.get(fromField)
        if (!fromValues || fromValues.size === 0) continue
        for (const toField of to.fields) {
          const confidence = relationshipConfidence(from, fromField, to, toField)
          if (!confidence) continue
          const toValues = to.valuesByField.get(toField)
          if (!toValues || toValues.size === 0) continue
          let matches = 0
          fromValues.forEach(value => {
            if (toValues.has(value)) matches += 1
          })
          if (matches === 0) continue
          if (confidence === 'possible' && matches < Math.min(2, fromValues.size)) continue
          relationships.push({
            fromTable: from.label,
            fromField,
            toTable: to.label,
            toField,
            matches,
            total: fromValues.size,
            confidence,
          })
        }
      }
    }
  }
  return relationships
    .sort((a, b) => {
      if (a.confidence !== b.confidence) return a.confidence === 'strong' ? -1 : 1
      return b.matches - a.matches || a.fromTable.localeCompare(b.fromTable) || a.toTable.localeCompare(b.toTable)
    })
    .slice(0, 24)
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

export default function DatabaseView({ workspacePath, hideHeader = false }: DatabaseViewProps) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [files, setFiles] = useState<PlannerFile[]>([])
  const [tableRowCounts, setTableRowCounts] = useState<Record<string, number>>({})
  const [tableColumns, setTableColumns] = useState<Record<string, string[]>>({})
  const [contentCache, setContentCache] = useState<Record<string, ContentCacheEntry | undefined>>({})
  const [relationships, setRelationships] = useState<DBRelationship[]>([])
  const [relationshipLoading, setRelationshipLoading] = useState(false)
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [searchTerm, setSearchTerm] = useState('')
  const [detailTab, setDetailTab] = useState<'preview' | 'schema' | 'raw'>('preview')
  const [maximizedCell, setMaximizedCell] = useState<MaximizedCell | null>(null)

  // The workflow's single SQLite database. Each table is surfaced as a pseudo
  // "file" entry (filepath `db/db.sqlite#<table>`) so the existing list/preview/
  // schema/relationship UI renders tables with no structural change.
  const dbFile = workspacePath ? `${workspacePath}/db/db.sqlite` : null

  const load = useCallback(async () => {
    if (!dbFile) return
    setLoading(true)
    setRelationshipLoading(false)
    setError(null)
    try {
      const resp = await agentApi.getWorkflowDBTables(dbFile)
      if (!resp.success || !resp.data) {
        // Most commonly: the workflow hasn't been migrated to SQLite yet.
        throw new Error(resp.error || 'No db/db.sqlite found for this automation.')
      }

      const tables = resp.data.tables.slice().sort((a, b) => a.name.localeCompare(b.name))
      const pseudoFiles: PlannerFile[] = tables.map(t => ({ filepath: `db/db.sqlite#${t.name}`, type: 'file' } as PlannerFile))
      setTableRowCounts(Object.fromEntries(tables.map(t => [`db/db.sqlite#${t.name}`, t.row_count])))
      setTableColumns(Object.fromEntries(tables.map(t => [`db/db.sqlite#${t.name}`, t.columns.map(column => column.name)])))
      const cache: Record<string, ContentCacheEntry> = {}
      for (const t of tables) {
        const path = `db/db.sqlite#${t.name}`
        const content = JSON.stringify(t.sample)
        const cols = t.columns.map(c => c.name)
        cache[path] = {
          content,
          formatted: JSON.stringify(t.sample, null, 2),
          summary: {
            kind: 'array',
            label: `${t.row_count} row${t.row_count === 1 ? '' : 's'}`,
            detail: cols.length > 0 ? `${cols.length} column${cols.length === 1 ? '' : 's'}: ${cols.slice(0, 6).join(', ')}${cols.length > 6 ? ', ...' : ''}` : undefined,
          },
        }
      }

      setFiles(pseudoFiles)
      setContentCache(cache)
      setSelectedPath(prev => {
        if (prev && pseudoFiles.some(file => file.filepath === prev)) return prev
        return null
      })

      // Relationship inference over the sample rows (same heuristic as before).
      const dbTables = tables
        .map(t => createTable(`db/db.sqlite#${t.name}`, t.name, t.sample))
        .filter((t): t is DBTable => t !== null)
      setRelationships(inferDBRelationships(dbTables))
      setLoading(false)
    } catch (err) {
      setFiles([])
      setTableRowCounts({})
      setTableColumns({})
      setRelationships([])
      const failure = describeDatabaseLoadFailure(err)
      setError(failure.missing ? null : failure.message)
      setLoading(false)
      setRelationshipLoading(false)
    }
  }, [dbFile])

  useEffect(() => {
    load()
  }, [load])

  const selectFile = useCallback(async (path: string) => {
    setSelectedPath(path)
    setDetailTab('preview')
    if (contentCache[path] === undefined) {
      const content = await readText(path)
      setContentCache(prev => ({ ...prev, [path]: buildContentCacheEntry(path, content) }))
    }
  }, [contentCache])

  const totalSize = useMemo(() => {
    const total = files.reduce((sum, file) => sum + (contentCache[file.filepath]?.content?.length ?? 0), 0)
    return total > 0 ? formatBytes(total) : null
  }, [contentCache, files])

  const filteredFiles = useMemo(() => {
    const q = searchTerm.trim().toLowerCase()
    if (!q) return files
    return files.filter(file => {
      const rel = workspacePath ? relativeDBPath(workspacePath, file.filepath) : fileName(file.filepath)
      const summary = contentCache[file.filepath]?.summary
      return rel.toLowerCase().includes(q) || summary?.label.toLowerCase().includes(q) || summary?.detail?.toLowerCase().includes(q)
    })
  }, [contentCache, files, searchTerm, workspacePath])

  const selectedFile = selectedPath ? files.find(file => file.filepath === selectedPath) ?? null : null
  const selectedCache = selectedPath ? contentCache[selectedPath] : undefined
  const selectedRel = selectedPath && workspacePath ? relativeDBPath(workspacePath, selectedPath) : selectedPath ? fileName(selectedPath) : ''
  const selectedSize = selectedFile ? formatBytes(selectedCache?.content?.length ?? plannerFileSize(selectedFile)) : null
  const selectedPreviews = useMemo(
    () => selectedPath ? buildTablePreviewsFromContent(workspacePath, selectedPath, selectedCache?.content ?? null) : [],
    [selectedCache?.content, selectedPath, workspacePath],
  )
  const selectedFields = useMemo(() => {
    if (selectedPath && tableColumns[selectedPath]?.length) return tableColumns[selectedPath]
    const fields = new Set<string>()
    for (const preview of selectedPreviews) {
      for (const column of preview.columns) fields.add(column)
    }
    return Array.from(fields).sort()
  }, [selectedPath, selectedPreviews, tableColumns])
  const selectedRelationships = useMemo(() => {
    if (!selectedRel) return []
    return relationships.filter(rel => rel.fromTable === selectedRel || rel.toTable === selectedRel || rel.fromTable.startsWith(`${selectedRel}.`) || rel.toTable.startsWith(`${selectedRel}.`))
  }, [relationships, selectedRel])

  return (
        <div className="flex h-full min-h-0 w-full flex-col bg-background">
          {!hideHeader && (
            <WorkspaceViewHeader
              icon={Table2}
              title="Database"
              context={<span className="text-xs text-muted-foreground">db/db.sqlite - tables</span>}
              actions={<WorkspaceViewIconButton label="Refresh database" onClick={load} disabled={loading} spinning={loading} />}
            />
          )}

          <div className="flex flex-shrink-0 flex-col gap-2 border-b border-border px-4 py-3 text-sm sm:flex-row sm:items-center">
            <div className="grid grid-cols-3 gap-2 text-xs sm:flex sm:items-center sm:gap-3">
              <div className="rounded-md border border-border bg-muted/20 px-2.5 py-1.5">
                <span className="text-muted-foreground">Tables</span>
                <span className="ml-2 font-semibold">{files.length}</span>
              </div>
              <div className="rounded-md border border-border bg-muted/20 px-2.5 py-1.5">
                <span className="text-muted-foreground">Relationships</span>
                <span className="ml-2 font-semibold">{relationships.length}</span>
              </div>
              <div className="rounded-md border border-border bg-muted/20 px-2.5 py-1.5">
                <span className="text-muted-foreground">Sample size</span>
                <span className="ml-2 font-semibold">{totalSize ?? '-'}</span>
              </div>
            </div>
          </div>

          <div className="flex min-h-0 flex-1 flex-col">
            {loading && files.length === 0 && (
              <div className="col-span-full flex items-center gap-2 p-4 text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                Loading database...
              </div>
            )}

            {error && (
              <div className="col-span-full m-4 flex items-start gap-2 rounded-md bg-destructive/10 p-3 text-destructive">
                <AlertCircle className="mt-0.5 h-4 w-4 flex-shrink-0" />
                <div className="text-sm">{error}</div>
              </div>
            )}

            {!loading && !error && files.length === 0 && (
              <div className="col-span-full py-12 text-center text-muted-foreground">
                <Table2 className="mx-auto mb-3 h-10 w-10 opacity-30" />
                <div className="text-sm">
                  Database is empty. Tables will appear here after this project creates durable data.
                </div>
              </div>
            )}

            {!error && files.length > 0 && (
              <>
                {!selectedPath && <aside className="flex min-h-0 flex-1 flex-col">
                  <div className="border-b border-border p-3">
                    <div className="relative">
                      <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                      <input
                        value={searchTerm}
                        onChange={event => setSearchTerm(event.target.value)}
                        placeholder="Search tables or columns"
                        aria-label="Search database tables or columns"
                        className="h-9 w-full rounded-md border border-input bg-background pl-8 pr-3 text-sm outline-none transition-colors focus:border-primary"
                      />
                    </div>
                  </div>
                  <div className="grid flex-1 content-start gap-2 overflow-y-auto p-3 sm:grid-cols-2">
                    {filteredFiles.map(file => {
                      const path = file.filepath
                      const cache = contentCache[path]
                      const rel = workspacePath ? relativeDBPath(workspacePath, path) : fileName(path)
                      const size = formatBytes(cache?.content?.length ?? plannerFileSize(file))
                      const isSelected = path === selectedPath
                      return (
                        <button
                          key={path}
                          onClick={() => selectFile(path)}
                          className={`flex w-full items-start gap-2 rounded-md border px-3 py-3 text-left transition-colors ${
                            isSelected
                              ? 'border-primary/40 bg-primary/10'
                              : 'border-transparent hover:border-border hover:bg-muted/50'
                          }`}
                        >
                          <FileJson className={`mt-0.5 h-4 w-4 flex-shrink-0 ${isSelected ? 'text-primary' : 'text-muted-foreground'}`} />
                          <span className="min-w-0 flex-1">
                            <span className="block truncate font-mono text-sm">{rel}</span>
                            <span className="mt-1 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
                              <span className="truncate">{cache?.summary.label ?? 'not loaded'}</span>
                              {size && <span className="flex-shrink-0">{size} sample</span>}
                            </span>
                          </span>
                        </button>
                      )
                    })}
                    {filteredFiles.length === 0 && (
                      <div className="col-span-full px-2 py-8 text-center text-sm text-muted-foreground">No matching tables.</div>
                    )}
                  </div>
                </aside>}

                {selectedPath && <section className="min-h-0 flex-1 overflow-y-auto p-4">
                    <WorkspacePanelBack label="Back to all tables" onBack={() => setSelectedPath(null)} />
                    <div className="space-y-4">
                      <div className="flex flex-col gap-3 rounded-md border border-border bg-muted/20 p-3 sm:flex-row sm:items-start sm:justify-between">
                        <div className="min-w-0">
                          <div className="flex items-center gap-2">
                            <FileJson className="h-4 w-4 flex-shrink-0 text-primary" />
                            <h3 className="truncate font-mono text-sm font-semibold">{selectedRel}</h3>
                          </div>
                          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                            <span>{selectedCache?.summary.kind ?? 'loading'}</span>
                            {selectedCache?.summary.label && <span>{selectedCache.summary.label}</span>}
                            {selectedSize && <span>{selectedSize} sample</span>}
                          </div>
                          {selectedCache?.summary.detail && (
                            <div className="mt-2 line-clamp-2 font-mono text-xs text-muted-foreground">{selectedCache.summary.detail}</div>
                          )}
                        </div>
                        {(relationshipLoading || selectedRelationships.length > 0) && (
                          <div className="flex flex-shrink-0 items-center gap-2 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs">
                            <Link2 className="h-3.5 w-3.5 text-primary" />
                            {relationshipLoading ? 'Scanning links' : `${selectedRelationships.length} link${selectedRelationships.length === 1 ? '' : 's'}`}
                          </div>
                        )}
                      </div>

                      <div className="flex flex-wrap gap-1 border-b border-border">
                        {[
                          ['preview', 'Rows'],
                          ['schema', 'Fields'],
                          ['raw', 'Raw JSON'],
                        ].map(([id, label]) => (
                          <button
                            key={id}
                            onClick={() => setDetailTab(id as 'preview' | 'schema' | 'raw')}
                            className={`px-3 py-2 text-sm transition-colors ${
                              detailTab === id
                                ? 'border-b-2 border-primary font-medium text-foreground'
                                : 'text-muted-foreground hover:text-foreground'
                            }`}
                          >
                            {label}
                          </button>
                        ))}
                      </div>

                      {selectedCache === undefined && (
                        <div className="flex items-center gap-2 text-sm text-muted-foreground">
                          <Loader2 className="h-4 w-4 animate-spin" />
                          Loading {selectedRel}...
                        </div>
                      )}

                      {selectedCache !== undefined && detailTab === 'preview' && (
                        <div className="space-y-3">
                          {selectedPreviews.length === 0 ? (
                            <div className="rounded-md border border-border bg-muted/20 p-4 text-sm text-muted-foreground">
                              No table-like rows detected. Use Raw JSON to inspect the file.
                            </div>
                          ) : (
                            selectedPreviews.map(preview => (
                              <div key={preview.id} className="rounded-md border border-border">
                                <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2 text-sm">
                                  <Table2 className="h-4 w-4 text-primary" />
                                  <span className="min-w-0 truncate font-mono font-medium">{preview.label}</span>
                                  <span className="ml-auto text-xs text-muted-foreground">
                                    {tableRowCounts[preview.id] && tableRowCounts[preview.id] > preview.rows.length
                                      ? `showing ${preview.rows.length} sampled rows of ${tableRowCounts[preview.id]} total`
                                      : `showing ${preview.rows.length} of ${preview.totalRows} rows`}
                                  </span>
                                </div>
                                <div className="overflow-auto">
                                  <table className="w-full min-w-[36rem] border-collapse text-xs">
                                    <thead className="bg-muted/40">
                                      <tr>
                                        {preview.columns.map(column => (
                                          <th key={column} className="border-b border-r border-border px-2 py-2 text-left font-mono font-medium last:border-r-0">
                                            {column}
                                          </th>
                                        ))}
                                      </tr>
                                    </thead>
                                    <tbody>
                                      {preview.rows.map((row, idx) => (
                                        <tr key={idx} className="odd:bg-background even:bg-muted/10">
                                          {preview.columns.map(column => {
                                            const rawValue = row[column]
                                            const cellText = formatCell(rawValue)
                                            const expandable = canMaximizeCell(rawValue, cellText)
                                            return (
                                              <td key={column} className="max-w-[18rem] border-b border-r border-border/60 px-2 py-1.5 align-top font-mono last:border-r-0" title={cellText}>
                                                <div className="group/cell flex min-w-0 items-start gap-1">
                                                  <span className="min-w-0 flex-1 truncate">
                                                    {cellText || <span className="text-muted-foreground">null</span>}
                                                  </span>
                                                  {expandable && (
                                                    <button
                                                      type="button"
                                                      onClick={() => setMaximizedCell({
                                                        table: preview.label,
                                                        row: idx + 1,
                                                        column,
                                                        preview: cellText || 'null',
                                                        detail: formatCellDetail(rawValue),
                                                      })}
                                                      className="mt-[-1px] inline-flex rounded p-0.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                                                      title="Maximize cell"
                                                      aria-label={`Maximize ${column} cell`}
                                                    >
                                                      <Maximize2 className="h-3 w-3" />
                                                    </button>
                                                  )}
                                                </div>
                                              </td>
                                            )
                                          })}
                                        </tr>
                                      ))}
                                    </tbody>
                                  </table>
                                </div>
                              </div>
                            ))
                          )}
                        </div>
                      )}

                      {selectedCache !== undefined && detailTab === 'schema' && (
                        <div className="space-y-3">
                          <div className="grid gap-2 sm:grid-cols-3">
                            <div className="rounded-md border border-border p-3">
                              <div className="text-xs text-muted-foreground">Shape</div>
                              <div className="mt-1 font-medium">{selectedCache.summary.kind}</div>
                            </div>
                            <div className="rounded-md border border-border p-3">
                              <div className="text-xs text-muted-foreground">Rows / keys</div>
                              <div className="mt-1 font-medium">{selectedCache.summary.label}</div>
                            </div>
                            <div className="rounded-md border border-border p-3">
                              <div className="text-xs text-muted-foreground">Detected fields</div>
                              <div className="mt-1 font-medium">{selectedFields.length}</div>
                            </div>
                          </div>
                          {selectedFields.length > 0 && (
                            <div className="rounded-md border border-border p-3">
                              <div className="mb-2 text-sm font-medium">Fields</div>
                              <div className="flex flex-wrap gap-1.5">
                                {selectedFields.map(field => (
                                  <span key={field} className="rounded border border-border bg-muted/30 px-2 py-1 font-mono text-xs">
                                    {field}
                                  </span>
                                ))}
                              </div>
                            </div>
                          )}
                          {(relationshipLoading || selectedRelationships.length > 0) && (
                            <div className="rounded-md border border-border p-3">
                              <div className="mb-2 flex items-center gap-2 text-sm font-medium">
                                <Link2 className="h-4 w-4 text-primary" />
                                Detected links
                                {relationshipLoading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
                              </div>
                              {selectedRelationships.length > 0 ? (
                                <div className="space-y-2">
                                  {selectedRelationships.map(rel => (
                                    <div key={`${rel.fromTable}:${rel.fromField}->${rel.toTable}:${rel.toField}`} className="rounded-md border border-border/70 bg-muted/20 px-2.5 py-2 text-xs">
                                      <div className="grid grid-cols-[minmax(0,1fr)_3.5rem_minmax(0,1fr)] items-center gap-2">
                                        <div className="min-w-0">
                                          <div className="truncate font-mono text-foreground">{rel.fromTable}</div>
                                          <div className="truncate font-mono text-muted-foreground">{rel.fromField}</div>
                                        </div>
                                        <div className="relative h-6" aria-hidden="true">
                                          <div className="absolute left-0 right-1 top-1/2 h-px -translate-y-1/2 bg-primary/50" />
                                          <div className="absolute right-0 top-1/2 h-2 w-2 -translate-y-1/2 rotate-45 border-r border-t border-primary/70" />
                                        </div>
                                        <div className="min-w-0 text-right">
                                          <div className="truncate font-mono text-foreground">{rel.toTable}</div>
                                          <div className="truncate font-mono text-muted-foreground">{rel.toField}</div>
                                        </div>
                                      </div>
                                      <div className="mt-1 text-muted-foreground">
                                        {rel.matches}/{rel.total} values match · {rel.confidence}
                                      </div>
                                    </div>
                                  ))}
                                </div>
                              ) : (
                                <div className="text-xs text-muted-foreground">No links detected for this file.</div>
                              )}
                            </div>
                          )}
                        </div>
                      )}

                      {selectedCache !== undefined && detailTab === 'raw' && (
                        selectedCache.formatted == null ? (
                          <div className="italic text-muted-foreground">File missing or empty.</div>
                        ) : (
                          <pre className="max-h-[34rem] overflow-auto rounded border border-border/60 bg-background p-3 text-[11px] leading-5 text-foreground">
                            {selectedCache.formatted}
                          </pre>
                        )
                      )}
                    </div>
                </section>}
              </>
            )}
          </div>

        {maximizedCell && (
          <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-black/60 p-3 sm:p-6" onMouseDown={() => setMaximizedCell(null)}>
            <div
              className="flex max-h-[calc(100dvh-2rem)] w-full max-w-4xl flex-col rounded-lg border border-border bg-background shadow-2xl"
              onMouseDown={event => event.stopPropagation()}
            >
              <div className="flex flex-shrink-0 items-start justify-between gap-3 border-b border-border p-3">
                <div className="min-w-0">
                  <div className="truncate font-mono text-sm font-semibold">{maximizedCell.column}</div>
                  <div className="mt-1 truncate text-xs text-muted-foreground">
                    {maximizedCell.table} · row {maximizedCell.row}
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => setMaximizedCell(null)}
                  className="rounded-md p-1 transition-colors hover:bg-muted"
                  title="Close"
                  aria-label="Close maximized cell"
                >
                  <X className="h-4 w-4" />
                </button>
              </div>
              <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs leading-5 text-foreground">
                {maximizedCell.detail}
              </pre>
            </div>
          </div>
        )}
      </div>
  )
}

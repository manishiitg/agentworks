import { useCallback, useEffect, useRef, useState } from 'react'
import type { PlannerFile } from '../../services/api-types'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeEntry, type KnowledgeFolder, type KnowledgeRead } from '../../services/knowledgebaseApi'
import type { ReadOnlyFileWorkspaceSource } from '../../components/workspace/fileWorkspaceSource'
import { BinaryFilePreview } from './BinaryFilePreview'

export function useKnowledgebaseFiles(revision: number, active: boolean, onFolder: (path: string) => void): ReadOnlyFileWorkspaceSource {
  const [files, setFiles] = useState<PlannerFile[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expandedFolders, setExpandedFolders] = useState(new Set<string>())
  const [read, setRead] = useState<KnowledgeRead | null>(null)
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [openTabs, setOpenTabs] = useState<Array<{ name: string; path: string }>>([])
  const [showFileContent, setShowFileContent] = useState(false)
  const [loadingFileContent, setLoadingFileContent] = useState(false)
  const [refreshKey, setRefreshKey] = useState(0)
  const readGeneration = useRef(0)
  const tabsRef = useRef(openTabs)
  tabsRef.current = openTabs
  const entryIndex = useRef(new Map<string, KnowledgeEntry>())
  const readController = useRef<AbortController | null>(null)
  const selectedRef = useRef(selectedPath)
  selectedRef.current = selectedPath

  const openFile = useCallback(async (path: string) => {
    readGeneration.current++
    const entry = entryIndex.current.get(path)
    readController.current?.abort()
    const controller = new AbortController(); readController.current = controller
    setRead(null); setSelectedPath(path); selectedRef.current = path; setShowFileContent(true); setLoadingFileContent(true); setError(null)
    onFolder(entry?.folder_path ?? path.split('/').slice(0, -1).join('/'))
    try {
      const result = entry ? await knowledgebaseApi.read(entry.entry_id, controller.signal) : await knowledgebaseApi.readPath(path, controller.signal)
      if (controller.signal.aborted) return
      entryIndex.current.set(path, result.entry)
      setRead(result)
      setOpenTabs(tabs => tabs.some(tab => tab.path === path) ? tabs : [...tabs, { name: result.entry.filename, path }])
    } catch (failure) {
      if (controller.signal.aborted) return
      setRead(null); setShowFileContent(false); setSelectedPath(null); selectedRef.current = null
      setOpenTabs(tabs => tabs.filter(tab => tab.path !== path)); setError(knowledgebaseError(failure))
    } finally { if (!controller.signal.aborted) setLoadingFileContent(false) }
  }, [onFolder])

  // Each folder's direct children are fetched once and cached. Expanding a folder fetches only that folder, and the
  // tree is rebuilt from the cache at once; a refresh refetches the root and every open folder in parallel. The old
  // version refetched the whole tree level by level on every click, so a folder looked dead and then filled all at
  // once (RTS 2026-10-06).
  const cache = useRef(new Map<string, { folders: KnowledgeFolder[]; entries: KnowledgeEntry[] }>())
  const expandedRef = useRef(expandedFolders)
  expandedRef.current = expandedFolders
  const treeController = useRef<AbortController | null>(null)

  const fetchFolder = useCallback(async (path: string, signal: AbortSignal) => {
    const [folders, entries] = await Promise.all([
      (async () => {
        const items: KnowledgeFolder[] = []; let cursor = ''
        do { const page = await knowledgebaseApi.folders(path, cursor, signal); items.push(...page.folders); cursor = page.next_cursor || '' } while (cursor)
        return items.filter(folder => folder.path !== path)
      })(),
      (async () => {
        const items: KnowledgeEntry[] = []; let cursor = ''
        do { const page = await knowledgebaseApi.entries({ folder_path: path, cursor, limit: 100 }, signal); items.push(...page.entries); cursor = page.next_cursor || '' } while (cursor)
        return items.filter(entry => entry.folder_path === path)
      })(),
    ])
    if (signal.aborted) return
    cache.current.set(path, { folders, entries })
    const next = new Map(entryIndex.current)
    for (const entry of entries) next.set(entry.path, entry)
    entryIndex.current = next
  }, [])

  const rebuild = useCallback(() => {
    const build = (path: string): PlannerFile[] => {
      const node = cache.current.get(path)
      if (!node) return []
      return [
        ...node.folders.map(folder => ({ filepath: folder.path, type: 'folder' as const, children: expandedRef.current.has(folder.path) ? build(folder.path) : [] })),
        ...node.entries.map(entry => ({ filepath: entry.path, type: 'file' as const, last_modified: entry.updated_at })),
      ]
    }
    setFiles(build(''))
  }, [])

  // Fetch any open folder that is not cached yet, then show it.
  const loadMissing = useCallback(async (paths: string[]) => {
    const missing = paths.filter(path => !cache.current.has(path))
    if (!missing.length) { rebuild(); return }
    const signal = treeController.current?.signal ?? new AbortController().signal
    setLoading(true)
    try {
      await Promise.all(missing.map(path => fetchFolder(path, signal)))
      if (!signal.aborted) rebuild()
    } catch (failure) {
      if (!signal.aborted) setError(knowledgebaseError(failure))
    } finally { if (!signal.aborted) setLoading(false) }
  }, [fetchFolder, rebuild])

  useEffect(() => {
    if (!active) return
    const readVersion = readGeneration.current
    treeController.current?.abort()
    const controller = new AbortController(); treeController.current = controller
    setLoading(true); setError(null)
    cache.current = new Map()
    void Promise.all(['', ...expandedRef.current].map(path => fetchFolder(path, controller.signal))).then(async () => {
      if (controller.signal.aborted) return
      rebuild()
      const next = entryIndex.current
      const paths = new Set(tabsRef.current.map(tab => tab.path))
      if (selectedRef.current) paths.add(selectedRef.current)
      await Promise.all([...paths].map(async path => {
        const entry = next.get(path)
        if (!entry) return
        try {
          const result = await knowledgebaseApi.read(entry.entry_id, controller.signal)
          if (!controller.signal.aborted && selectedRef.current === path && readGeneration.current === readVersion) setRead(result)
        } catch (failure) {
          if (controller.signal.aborted || readGeneration.current !== readVersion) return
          entryIndex.current.delete(path)
          setOpenTabs(tabs => tabs.filter(tab => tab.path !== path))
          if (selectedRef.current === path) {
            readController.current?.abort(); setRead(null); setSelectedPath(null); selectedRef.current = null; setShowFileContent(false); setError(knowledgebaseError(failure))
          }
        }
      }))
    }).catch(failure => {
      if (!controller.signal.aborted) {
        entryIndex.current.clear(); readController.current?.abort(); setFiles([]); setRead(null); setSelectedPath(null); selectedRef.current = null; setOpenTabs([]); setShowFileContent(false); setError(knowledgebaseError(failure))
      }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
    // expandedFolders is read through expandedRef: opening a folder must not refetch the whole tree.
  }, [revision, refreshKey, active, fetchFolder, rebuild])
  useEffect(() => () => readController.current?.abort(), [])

  function revealFolder(path: string) {
    onFolder(path)
    setShowFileContent(false)
    const next = new Set(expandedRef.current); const parts = path.split('/')
    for (let i = 1; i <= parts.length; i++) next.add(parts.slice(0, i).join('/'))
    expandedRef.current = next; setExpandedFolders(next)
    void loadMissing([...next])
  }
  return {
    files, loading, error, expandedFolders,
    selectedFile: selectedPath ? { name: selectedPath.split('/').pop() || selectedPath, path: selectedPath } : null,
    fileContent: read?.entry.binary ? '' : read?.content || '', loadingFileContent, showFileContent, setShowFileContent,
    openTabs, openFile, revealFolder,
    closeFile: path => {
      setOpenTabs(tabs => tabs.filter(tab => tab.path !== path))
      if (selectedPath === path) { readController.current?.abort(); setSelectedPath(null); selectedRef.current = null; setRead(null); setShowFileContent(false) }
    },
    toggleFolder: path => {
      onFolder(path)
      const next = new Set(expandedRef.current)
      if (next.has(path)) next.delete(path); else next.add(path)
      expandedRef.current = next; setExpandedFolders(next)
      if (next.has(path)) void loadMissing([path]); else rebuild()
    },
    refresh: () => setRefreshKey(value => value + 1),
    contentHeader: read && <div className="border-b border-border px-4 py-2 text-xs text-muted-foreground">{read.entry.type} · Updated {read.entry.updated_at ? new Date(read.entry.updated_at).toLocaleString() : 'recently'}{read.entry.updated_by && ` by ${read.entry.updated_by}`}{read.entry.binary && read.content_base64 && <BinaryFilePreview name={read.entry.filename} base64={read.content_base64} size={read.size} />}</div>,
  }
}

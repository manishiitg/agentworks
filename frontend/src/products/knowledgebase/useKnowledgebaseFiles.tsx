import { useCallback, useEffect, useRef, useState } from 'react'
import type { PlannerFile } from '../../services/api-types'
import { knowledgebaseApi, knowledgebaseError, type KnowledgeEntry, type KnowledgeRead } from '../../services/knowledgebaseApi'
import type { ReadOnlyFileWorkspaceSource } from '../../components/workspace/fileWorkspaceSource'

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
    if (!entry) return
    readController.current?.abort()
    const controller = new AbortController(); readController.current = controller
    setRead(null); setSelectedPath(path); selectedRef.current = path; setShowFileContent(true); setLoadingFileContent(true); setError(null)
    onFolder(entry.folder_path)
    try {
      const result = await knowledgebaseApi.read(entry.entry_id, controller.signal)
      if (controller.signal.aborted) return
      setRead(result)
      setOpenTabs(tabs => tabs.some(tab => tab.path === path) ? tabs : [...tabs, { name: entry.filename, path }])
    } catch (failure) {
      if (controller.signal.aborted) return
      setRead(null); setShowFileContent(false); setSelectedPath(null); selectedRef.current = null
      setOpenTabs(tabs => tabs.filter(tab => tab.path !== path)); setError(knowledgebaseError(failure))
    } finally { if (!controller.signal.aborted) setLoadingFileContent(false) }
  }, [onFolder])

  useEffect(() => {
    if (!active) return
    const readVersion = readGeneration.current
    const controller = new AbortController()
    setLoading(true); setError(null)
    const index = new Map<string, KnowledgeEntry>()
    async function folderTree(path: string): Promise<PlannerFile[]> {
      const [folders, entries] = await Promise.all([
        (async () => {
          const items = []; let cursor = ''
          do { const page = await knowledgebaseApi.folders(path, cursor, controller.signal); items.push(...page.folders); cursor = page.next_cursor || '' } while (cursor)
          return items
        })(),
        (async () => {
          const items = []; let cursor = ''
          do { const page = await knowledgebaseApi.entries({ folder_path: path, cursor, limit: 100 }, controller.signal); items.push(...page.entries); cursor = page.next_cursor || '' } while (cursor)
          return items.filter(entry => entry.folder_path === path)
        })(),
      ])
      for (const entry of entries) index.set(entry.path, entry)
      const children = await Promise.all(folders.filter(folder => folder.path !== path).map(async folder => ({ filepath: folder.path, type: 'folder' as const, children: expandedFolders.has(folder.path) ? await folderTree(folder.path) : [] })))
      return [...children, ...entries.map(entry => ({ filepath: entry.path, type: 'file' as const, last_modified: entry.updated_at }))]
    }
    void folderTree('').then(async tree => {
      if (controller.signal.aborted) return
      // Preserve open tab IDs for fresh authorized reads even when their folder is collapsed.
      const next = new Map(entryIndex.current)
      for (const [path, entry] of index) next.set(path, entry)
      entryIndex.current = next; setFiles(tree)
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
  }, [revision, refreshKey, expandedFolders, active])
  useEffect(() => () => readController.current?.abort(), [])

  function revealFolder(path: string) {
    onFolder(path)
    setShowFileContent(false)
    setExpandedFolders(current => {
      const next = new Set(current); const parts = path.split('/')
      for (let i = 1; i <= parts.length; i++) next.add(parts.slice(0, i).join('/'))
      return next
    })
  }
  return {
    files, loading, error, expandedFolders,
    selectedFile: selectedPath ? { name: selectedPath.split('/').pop() || selectedPath, path: selectedPath } : null,
    fileContent: read?.content || '', loadingFileContent, showFileContent, setShowFileContent,
    openTabs, openFile, revealFolder,
    closeFile: path => {
      setOpenTabs(tabs => tabs.filter(tab => tab.path !== path))
      if (selectedPath === path) { readController.current?.abort(); setSelectedPath(null); selectedRef.current = null; setRead(null); setShowFileContent(false) }
    },
    toggleFolder: path => { onFolder(path); setExpandedFolders(current => { const next = new Set(current); if (next.has(path)) next.delete(path); else next.add(path); return next }) },
    refresh: () => setRefreshKey(value => value + 1),
    contentHeader: read && <div className="border-b border-border px-4 py-2 text-xs text-muted-foreground">{read.entry.type} · Updated {read.entry.updated_at ? new Date(read.entry.updated_at).toLocaleString() : 'recently'}{read.entry.updated_by && ` by ${read.entry.updated_by}`}</div>,
  }
}

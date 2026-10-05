import { readWorkspaceViewPreference, writeWorkspaceViewPreference } from '../../../utils/workspaceViewPreference'
import { create } from 'zustand'
import type { Activity, DrawerTab, TreeNode, WsFile } from './types'
import { resolveSetState, type SetStateAction } from './storeUtils'

// Parent-side workspace browsing: drawer tabs, the file tree/viewer, and the
// generated HTML documents (the progress page).
interface WorkspaceState {
  drawerTab: DrawerTab
  setDrawerTab: (v: SetStateAction<DrawerTab>) => void
  treeNodes: TreeNode[]
  setTreeNodes: (v: SetStateAction<TreeNode[]>) => void
  wsFiles: WsFile[]
  setWsFiles: (v: SetStateAction<WsFile[]>) => void
  allFiles: string[]
  setAllFiles: (v: SetStateAction<string[]>) => void
  viewerPath: string | null
  setViewerPath: (v: SetStateAction<string | null>) => void
  // Bumped whenever open_file fires, even for the SAME path — Quill may edit a
  // file and re-open it, and setting viewerPath to an unchanged string wouldn't
  // otherwise trigger a refetch.
  viewerRefreshKey: number
  setViewerRefreshKey: (v: SetStateAction<number>) => void
  viewerImageList: string[]
  setViewerImageList: (v: SetStateAction<string[]>) => void
  viewerContent: { isText: boolean; content: string } | null
  setViewerContent: (v: SetStateAction<{ isText: boolean; content: string } | null>) => void
  mapRefreshKey: number
  setMapRefreshKey: (v: SetStateAction<number>) => void
  progressHtml: string | null
  setProgressHtml: (v: SetStateAction<string | null>) => void
  wsRefreshKey: number
  setWsRefreshKey: (v: SetStateAction<number>) => void
  filesSubjectFilter: string
  setFilesSubjectFilter: (v: SetStateAction<string>) => void
  // The Files/Workspace tab defaults to grouping by subject → topic; 'date'
  // instead sorts every activity into one flat, most-recent-first list.
  filesGroupBy: 'subject' | 'date'
  setFilesGroupBy: (v: SetStateAction<'subject' | 'date'>) => void
  // Every activity (<Subject>/<Topic>/<slug>/activity.json) — the Files tab
  // groups these by their own subject/topic, no path-parsing needed.
  activities: Activity[]
  setActivities: (v: SetStateAction<Activity[]>) => void
}

function normalizeDrawerTab(value: unknown): DrawerTab | null {
  if (typeof value !== 'string') return null
  return ['assets', 'progress', 'files', 'allfiles', 'uploaded', 'browser'].includes(value) || value.startsWith('pin:')
    ? value as DrawerTab : null
}

export const useSparkQuillWorkspaceStore = create<WorkspaceState>()((set) => ({
  drawerTab: readWorkspaceViewPreference('sparkquill', 'main', normalizeDrawerTab) ?? 'progress',
  setDrawerTab: (v) => set((s) => {
    const drawerTab = resolveSetState(v, s.drawerTab)
    writeWorkspaceViewPreference('sparkquill', 'main', drawerTab)
    return { drawerTab }
  }),
  treeNodes: [],
  setTreeNodes: (v) => set((s) => ({ treeNodes: resolveSetState(v, s.treeNodes) })),
  wsFiles: [],
  setWsFiles: (v) => set((s) => ({ wsFiles: resolveSetState(v, s.wsFiles) })),
  allFiles: [],
  setAllFiles: (v) => set((s) => ({ allFiles: resolveSetState(v, s.allFiles) })),
  viewerPath: null,
  setViewerPath: (v) => set((s) => ({ viewerPath: resolveSetState(v, s.viewerPath) })),
  viewerRefreshKey: 0,
  setViewerRefreshKey: (v) => set((s) => ({ viewerRefreshKey: resolveSetState(v, s.viewerRefreshKey) })),
  viewerImageList: [],
  setViewerImageList: (v) => set((s) => ({ viewerImageList: resolveSetState(v, s.viewerImageList) })),
  viewerContent: null,
  setViewerContent: (v) => set((s) => ({ viewerContent: resolveSetState(v, s.viewerContent) })),
  mapRefreshKey: 0,
  setMapRefreshKey: (v) => set((s) => ({ mapRefreshKey: resolveSetState(v, s.mapRefreshKey) })),
  progressHtml: null,
  setProgressHtml: (v) => set((s) => ({ progressHtml: resolveSetState(v, s.progressHtml) })),
  wsRefreshKey: 0,
  setWsRefreshKey: (v) => set((s) => ({ wsRefreshKey: resolveSetState(v, s.wsRefreshKey) })),
  filesSubjectFilter: '',
  setFilesSubjectFilter: (v) => set((s) => ({ filesSubjectFilter: resolveSetState(v, s.filesSubjectFilter) })),
  filesGroupBy: 'subject',
  setFilesGroupBy: (v) => set((s) => ({ filesGroupBy: resolveSetState(v, s.filesGroupBy) })),
  activities: [],
  setActivities: (v) => set((s) => ({ activities: resolveSetState(v, s.activities) })),
}))

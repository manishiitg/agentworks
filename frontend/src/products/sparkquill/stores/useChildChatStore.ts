import { create } from 'zustand'
import type { Activity } from './types'
import { resolveSetState, type SetStateAction } from './storeUtils'

// Child Mode's workspace state. The conversation itself lives in the shared
// chat store (ChildPlatformChat mounts AgentWorks' ChatArea); this keeps what
// is SparkQuill's own: the bound activity and her inline viewer.
interface ChildChatState {
  // The ONE activity the child is currently bound to (current-activity.json)
  // — the child workspace shows only this, not every activity ever created.
  childActivity: Activity | null
  setChildActivity: (v: SetStateAction<Activity | null>) => void
  childViewerPath: string | null
  setChildViewerPath: (v: SetStateAction<string | null>) => void
  childTreeRefreshKey: number
  setChildTreeRefreshKey: (v: SetStateAction<number>) => void
}

export const useChildChatStore = create<ChildChatState>()((set) => ({
  childActivity: null,
  setChildActivity: (v) => set((s) => ({ childActivity: resolveSetState(v, s.childActivity) })),
  childViewerPath: null,
  setChildViewerPath: (v) => set((s) => ({ childViewerPath: resolveSetState(v, s.childViewerPath) })),
  childTreeRefreshKey: 0,
  setChildTreeRefreshKey: (v) => set((s) => ({ childTreeRefreshKey: resolveSetState(v, s.childTreeRefreshKey) })),
}))

import { useEffect, useState } from 'react'
import { Globe } from 'lucide-react'
import api from '../../services/api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useChromeExtensionConnection } from '../workflow/ChromeExtensionConnection'
import { WorkspaceContextRow } from './WorkspaceContextCard'

/** Read the same saved choice as BrowserWorkspacePanel; never configure or connect here. */
export function WorkspaceContextBrowser({
  workspacePath,
  profileId,
  onOpen,
  readOnly = false,
}: {
  workspacePath: string
  profileId: string
  onOpen: () => void
  readOnly?: boolean
}) {
  const extension = useChromeExtensionConnection(workspacePath, profileId, readOnly)
  const [tick, setTick] = useState(0)
  const [snapshot, setSnapshot] = useState<{ key: string; label: string } | null>(null)
  const key = `${profileId}:${workspacePath}`
  useLiveRefetch(() => setTick((value) => value + 1), {
    kinds: ['browser'],
    workflow: workspacePath,
    fallbackMs: 30000,
  })
  useEffect(() => {
    const controller = new AbortController()
    void api
      .get<{ mode: string; port: number }>('/api/browser/workspace', {
        params: { workspace_path: workspacePath, profile_id: profileId },
        skipSessionContext: true,
        signal: controller.signal,
      })
      .then(({ data }) => {
        if (!controller.signal.aborted)
          setSnapshot({
            key,
            label:
              data.mode === 'cdp'
                ? `Chrome · port ${data.port}`
                : data.mode === 'headless'
                  ? 'Workspace browser'
                  : data.mode === 'auto'
                    ? 'Automatic'
                    : 'Not selected',
          })
      })
      .catch(() => {
        if (!controller.signal.aborted) setSnapshot({ key, label: 'Unavailable' })
      })
    return () => controller.abort()
  }, [key, workspacePath, profileId, tick])
  const selected = extension.status.browsers?.find((browser) => browser.id === extension.status.browser_id)
  const label = extension.status.selected
    ? `${selected?.name || 'Chrome / Edge extension'} · ${extension.status.connected ? 'Connected' : 'Offline'}`
    : extension.loading
      ? 'Loading…'
      : extension.error
        ? 'Unavailable'
        : snapshot?.key === key
          ? snapshot.label
          : 'Loading…'
  return (
    <WorkspaceContextRow icon={Globe} detail={label} onClick={onOpen}>
      Browser
    </WorkspaceContextRow>
  )
}

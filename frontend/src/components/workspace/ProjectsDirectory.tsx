import { useCallback, useEffect, useState } from 'react'
import { Folder } from 'lucide-react'
import { LocalSetup, LOCAL_SETUP_SEEN_KEY } from '../LocalSetup'
import { isGuideRemembered, rememberGuide } from '../../utils/onboarding'
import { OPEN_LOCAL_SETUP_EVENT, takeLocalSetupRequest } from '../../utils/localSetupNavigation'
import { ProjectsWelcome } from './ProjectsWelcome'
import { DesktopConnections } from './DesktopConnections'
import { useWorkspaceProjects } from './WorkspaceProjectsProvider'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { WorkspacePageContent } from './WorkspacePageContent'

export default function ProjectsDirectory() {
  const { projects, canBrowse, loading, error, retry } = useWorkspaceProjects()
  // The Mac setup guide opens once on this Mac's first visit, and again from the account menu.
  const onThisMac = window.electronAPI?.getDesktopConnection?.() === 'local'
  const [setupOpen, setSetupOpen] = useState(() => takeLocalSetupRequest() || (onThisMac && !isGuideRemembered(LOCAL_SETUP_SEEN_KEY)))
  const closeSetup = useCallback(() => { rememberGuide(LOCAL_SETUP_SEEN_KEY); setSetupOpen(false) }, [])
  useEffect(() => {
    const open = () => { if (takeLocalSetupRequest()) setSetupOpen(true) }
    window.addEventListener(OPEN_LOCAL_SETUP_EVENT, open)
    return () => window.removeEventListener(OPEN_LOCAL_SETUP_EVENT, open)
  }, [])
  return <main className="flex h-full min-h-0 flex-col bg-background">
    <WorkspaceTopToolbar navigationKind="page" className="shrink-0">
      <Folder aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
      <h1 className="text-sm font-semibold">Projects</h1>
    </WorkspaceTopToolbar>
    <div className="min-h-0 flex-1 overflow-y-auto">
      <WorkspacePageContent width="form" className="mx-auto space-y-4">
        {canBrowse ? <>
          {!loading && !error && projects.length === 0 && <ProjectsWelcome />}

          <DesktopConnections directory projects={projects}>
            {loading ? <p role="status" className="text-sm text-muted-foreground">Loading projects…</p>
              : error ? <button type="button" className="text-sm text-muted-foreground underline" onClick={retry}>Could not load projects. Try again.</button>
              : null}
          </DesktopConnections>
        </> : <p className="text-sm text-muted-foreground">Projects aren’t available to this account. Use <button type="button" className="underline" onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher'))}>Search</button> to find the work you can access.</p>}
      </WorkspacePageContent>
    </div>
    {setupOpen && <LocalSetup onClose={closeSetup} />}
  </main>
}

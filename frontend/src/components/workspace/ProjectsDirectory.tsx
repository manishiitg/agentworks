import { Folder } from 'lucide-react'
import { DesktopConnections } from './DesktopConnections'
import { useWorkspaceProjects } from './WorkspaceProjectsProvider'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { WorkspacePageContent } from './WorkspacePageContent'

export default function ProjectsDirectory() {
  const { projects, canBrowse, loading, error, retry } = useWorkspaceProjects()
  return <main className="flex h-full min-h-0 flex-col bg-background">
    <WorkspaceTopToolbar navigationKind="page" className="shrink-0">
      <Folder aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
      <h1 className="text-sm font-semibold">Projects</h1>
    </WorkspaceTopToolbar>
    <div className="min-h-0 flex-1 overflow-y-auto">
      <WorkspacePageContent width="form" className="mx-auto space-y-4">
        {canBrowse ? <>
          <p className="text-sm text-muted-foreground">Find and pin the projects you work in.</p>
          <DesktopConnections directory projects={projects}>
            {loading ? <p role="status" className="text-sm text-muted-foreground">Loading projects…</p>
              : error ? <button type="button" className="text-sm text-muted-foreground underline" onClick={retry}>Could not load projects. Try again.</button>
              : null}
          </DesktopConnections>
        </> : <p className="text-sm text-muted-foreground">Projects aren’t available to this account. Use <button type="button" className="underline" onClick={() => window.dispatchEvent(new CustomEvent('open-quick-switcher'))}>Search</button> to find the work you can access.</p>}
      </WorkspacePageContent>
    </div>
  </main>
}

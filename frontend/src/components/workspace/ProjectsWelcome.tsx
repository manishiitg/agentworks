import { Plus } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { openQuickNavigation, quickNavigationItems } from '../../utils/quickNavigation'
import { openWorkspaceSettings } from '../../utils/workspaceSettingsNavigation'
import WelcomeAiReadiness from '../WelcomeAiReadiness'
import { Button } from '../ui/Button'

/** First use: the empty Projects page shows one next step at a time, an AI account first and then a first project. */
export function ProjectsWelcome() {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const createItem = quickNavigationItems(user, 'work', multiUser).find(item => item.action === 'create' && item.surface === 'work')
  return <section aria-labelledby="projects-welcome-heading" className="space-y-4 pt-2">
    <div>
      <h2 id="projects-welcome-heading" className="text-xl font-semibold tracking-tight">Welcome. Let’s start your first project.</h2>
      <p className="mt-1 text-sm text-muted-foreground">A project is where you and your agents work on one thing together, alone or with your team.</p>
    </div>
    <WelcomeAiReadiness products={['work']} onSetup={() => openWorkspaceSettings('providers')} crewReadyView={createItem && <section aria-label="Create a project" className="flex flex-col justify-between gap-4 rounded-2xl border border-border bg-card p-4 sm:flex-row sm:items-center sm:p-5">
      <div>
        <h3 className="text-sm font-semibold">Create a project</h3>
        <p className="mt-1 max-w-xl text-xs leading-5 text-muted-foreground">Say what it is for; its agent starts working with you in its chat.</p>
      </div>
      <Button type="button" onClick={() => openQuickNavigation(createItem)}><Plus className="mr-2 h-4 w-4" aria-hidden="true" />New project</Button>
    </section>} />
  </section>
}

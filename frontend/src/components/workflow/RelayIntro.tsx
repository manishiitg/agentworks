import { Bot, GitBranch, Repeat2, Workflow } from 'lucide-react'
import { ProductIntro } from '../ProductIntro'
import { useAuthStore } from '../../stores/useAuthStore'
import { useCommandDialogStore } from '../../stores/useCommandDialogStore'
import { hasProductCreateAccess } from '../../utils/workflowPermissions'

export function RelayIntro() {
  const canCreate = useAuthStore(state => hasProductCreateAccess(state.user, state.isMultiUserMode, 'relays'))
  return (
    <ProductIntro
      tour="relay-empty-state"
      product="Flows"
      icon={<Workflow className="h-9 w-9 text-gray-600 dark:text-gray-200" />}
      title="Run the same steps every time"
      description="Combine repeatable steps, AI tasks and decisions in one process. Describe what you need; the builder connects the steps for you."
      features={[
        { icon: Repeat2, title: 'Repeatable steps', description: 'Move data, check details and call your apps in a dependable order.' },
        { icon: Bot, title: 'AI where you need it', description: 'Let an agent read a document, extract information or prepare a response.' },
        { icon: GitBranch, title: 'Choose the next step', description: 'Sort a request by its topic or urgency, then send it down the right path.' },
      ]}
      footer="Example: a request arrives → AI understands it → checks choose the right team → your app receives the result. Developers can also build the steps in Python."
      createTour="relay-create"
      createLabel="Create your first Flow"
      canCreate={canCreate}
      createUnavailableMessage="Choose an existing Flow from the top bar, or ask an administrator to enable creation."
      onCreate={() => useCommandDialogStore.getState().openDialog('presetCreate')}
    />
  )
}

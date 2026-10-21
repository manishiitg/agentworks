import { CheckCircle2, MessageSquareText, Repeat2, Workflow } from 'lucide-react'
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
      description="Describe the work once. Your website or CRM can start it whenever it is needed."
      features={[
        { icon: MessageSquareText, title: 'Describe the work', description: 'Tell the builder what starts the work and what should happen.' },
        { icon: CheckCircle2, title: 'Try an example', description: 'Check the result before you put it to work.' },
        { icon: Repeat2, title: 'Use it again', description: 'Connect your app to the published Flow.' },
      ]}
      footer="Example: a support request arrives → identify the issue → send it to the right team."
      createTour="relay-create"
      createLabel="Create your first Flow"
      canCreate={canCreate}
      createUnavailableMessage="Choose an existing Flow from the top bar, or ask an administrator to enable creation."
      onCreate={() => useCommandDialogStore.getState().openDialog('presetCreate')}
    />
  )
}

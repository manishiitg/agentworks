import { Workflow } from 'lucide-react'
import { ProductIntro } from '../ProductIntro'
import { useAuthStore } from '../../stores/useAuthStore'
import { useCommandDialogStore } from '../../stores/useCommandDialogStore'
import { hasWorkflowCreateAccess } from '../../utils/workflowPermissions'

export function RelayIntro() {
  const canCreate = useAuthStore(state => hasWorkflowCreateAccess(state.user, state.isMultiUserMode))
  return (
    <ProductIntro
      tour="relay-empty-state"
      product="Relays"
      icon={<Workflow className="h-9 w-9 text-gray-600 dark:text-gray-200" />}
      title="Turn an idea into an API you can reuse"
      description="Describe an agent or a chain of agents in chat. Choose the information it receives and the result it returns, then connect it to your website or product."
      features={[
        { title: 'Build in chat', description: 'Describe the steps, tools and decisions you need. The builder creates them and shows a visual graph.' },
        { title: 'Test your draft', description: 'Try sample inputs, see what each agent did and review the final result. Refine it in chat.' },
        { title: 'Publish a version', description: 'Call a published version through API triggers while editing the next draft.' },
      ]}
      footer="Example: receive a support request → classify it → route it to an agent → return a JSON response."
      createTour="relay-create"
      createLabel="Create your first relay"
      canCreate={canCreate}
      createUnavailableMessage="Choose an existing relay from the top bar, or ask an administrator to enable creation."
      onCreate={() => useCommandDialogStore.getState().openDialog('presetCreate')}
    />
  )
}

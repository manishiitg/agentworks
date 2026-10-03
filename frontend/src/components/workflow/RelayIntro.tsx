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
      description="Build a graph of agents, scripts and decisions in chat. Define its inputs and outputs, then call it from your website or product."
      features={[
        { title: 'Build in chat', description: 'Connect agents, Python scripts and branches. Set each agent’s prompts, tools and model, and view the graph beside chat.' },
        { title: 'Test your draft', description: 'Run with sample inputs, inspect each step’s output and execution logs, and refine the graph in chat.' },
        { title: 'Publish a version', description: 'Call a published version through the API while editing the next draft. Add triggers or schedules when needed.' },
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

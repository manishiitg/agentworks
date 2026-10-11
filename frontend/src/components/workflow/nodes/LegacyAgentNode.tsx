import { memo } from 'react'
import type { LegacyAgentNodeData, AgentNodeData } from '../hooks/usePlanToFlow'
import { legacyTodoMessagesAsSequenceItems, isLegacyAgentStep } from '../../../utils/stepConfigMatching'
import { AgentNode } from './AgentNode'

interface LegacyAgentNodeProps {
  data: LegacyAgentNodeData
  selected?: boolean
}

// Compatibility adapter for stale saved canvas data. New Plan Design graphs no
// longer create `todo_task` nodes; both former orchestrators and authored
// agents are emitted as the single `agent` Agent type.
export const LegacyAgentNode = memo(({ data, selected }: LegacyAgentNodeProps) => {
  const step = data.step
  const agentData: AgentNodeData = {
    ...data,
    description: step.description,
    items: isLegacyAgentStep(step) ? legacyTodoMessagesAsSequenceItems(step) : []
  }

  return <AgentNode data={agentData} selected={selected} />
})

LegacyAgentNode.displayName = 'LegacyAgentNode'
export default LegacyAgentNode

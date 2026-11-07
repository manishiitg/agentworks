import { StepNode } from './StepNode'
import { RoutingStepNode } from './RoutingStepNode'
import { AgentNode } from './AgentNode'
import { StartNode, EndNode } from './StartEndNodes'
import { VariablesNode } from './VariablesNode'
import { WorkflowArtifactNode } from './WorkflowArtifactNode'

export { StepNode } from './StepNode'
export { RoutingStepNode } from './RoutingStepNode'
export { AgentNode } from './AgentNode'
export { StartNode, EndNode } from './StartEndNodes'
export { VariablesNode } from './VariablesNode'
export { WorkflowArtifactNode } from './WorkflowArtifactNode'

// Node types map for React Flow
export const nodeTypes = {
  step: StepNode,
  routing: RoutingStepNode,
  // Branch shares the mechanics but RoutingStepNode renders its own branch
  // icon and label, keeping the semantic distinction visible on the canvas.
  branch: RoutingStepNode,
  agent: AgentNode,
  start: StartNode,
  end: EndNode,
  variables: VariablesNode,
  'workflow-artifact': WorkflowArtifactNode
} as const

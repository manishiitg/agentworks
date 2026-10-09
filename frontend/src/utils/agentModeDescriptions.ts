/**
 * Centralized utility for agent mode descriptions
 * This eliminates code duplication across components
 */

// 'multi-agent' is the generic direct-chat mode (model + skills + tools +
// workspace context) — not tied to any single product or persona. The base
// agentworks surface uses it by default, and product surfaces that want
// plain chat instead of their own mode opt into it explicitly (see
// VideoStudioSurface.tsx calling setAgentMode('multi-agent')).
// There is no "Chief of Staff" product on main; that only exists on the
// separate, unmerged feature/chief-of-staff-product branch.
export type AgentMode = 'multi-agent' | 'workflow'

export const getAgentModeDescription = (agentMode: AgentMode): string => {
  switch (agentMode) {
    case 'workflow':
      return 'Todo-list-based automation execution with human verification and sequential task completion'
    case 'multi-agent':
    default:
      return 'Direct chat with the selected model, skills, tools, and workspace context'
  }
}

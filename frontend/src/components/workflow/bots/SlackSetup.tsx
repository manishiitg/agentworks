import type { ReactNode } from 'react'
import type { WorkflowBots } from './useWorkflowBots'
import { ProductSlackPanel } from '../../integrations/ProductSlackPanel'

/** Automation hosts use the same page as the product menu. Credentials live in My Vault. */
export function SlackSetup({ bots }: { bots: Pick<WorkflowBots, 'slackTarget'>; headerAction?: ReactNode; homeTabAction?: ReactNode; ownBotOnly?: boolean }) {
 const target = bots.slackTarget
 return target ? <ProductSlackPanel workspacePath={target.workspace_path} noun={target.profile_id ? 'Crew' : 'Goal'} /> : <p className="text-xs text-muted-foreground">Open a Crew or Goal to set up Slack.</p>
}

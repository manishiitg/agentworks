import { parsePlaybookSetupProgress } from './playbookSetupProgress'
export type { PlaybookSetupProgress as WebsiteGrowthSetupProgress } from './playbookSetupProgress'
export const parseWebsiteGrowthSetupProgress = (content: string, version: string, expectedChecks: readonly string[]) =>
  parsePlaybookSetupProgress(content, 'website-growth-loop', version, expectedChecks)

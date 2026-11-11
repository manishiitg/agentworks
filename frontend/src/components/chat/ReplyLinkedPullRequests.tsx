import { GitPullRequestIcon } from '../icons/octicons'
import { PRWatcherControls } from './PRWatcherControls'
import type { ReplyFileGroup, ReplyLinkedPullRequest } from '../../utils/replyFileChanges'

// Pull requests the server linked to this reply's turn (its tool calls opened
// or pushed to them), shown even when the reply changed no files. A PR whose
// repository and branch the reply's file card already covers is left to the card.
export function ReplyLinkedPullRequests({ pullRequests, sessionId, cardGroups }: { pullRequests: ReplyLinkedPullRequest[]; sessionId: string; cardGroups?: ReplyFileGroup[] }) {
  const covered = (pr: ReplyLinkedPullRequest) => !!pr.branch && !!cardGroups?.some(group => group.branch === pr.branch && group.github_repo?.toLowerCase() === pr.repository.toLowerCase())
  const shown = pullRequests.filter(pr => pr.number && pr.url && !covered(pr))
  if (!shown.length) return null
  return <div className="mt-1 flex flex-col gap-1 text-xs" aria-label="Pull requests from this turn">
    {shown.map(pr => <div key={`${pr.repository}#${pr.number}`} className="flex flex-wrap items-center gap-x-3 text-muted-foreground">
      <a className="inline-flex items-center gap-1 underline hover:text-foreground" href={pr.url} target="_blank" rel="noreferrer" title={pr.title}>
        <GitPullRequestIcon className="h-3.5 w-3.5" />PR #{pr.number}{pr.state === 'open' ? '' : ` ${pr.state}`} · {pr.repository}
      </a>
      <PRWatcherControls compact group={{ root: '', github_repo: pr.repository, pr: { number: pr.number, url: pr.url, state: pr.state } }} sessionId={sessionId} />
    </div>)}
  </div>
}

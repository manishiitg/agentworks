import { GoalAfterRunToggle } from './GoalAfterRunToggle'
import React, { useCallback, useEffect, useState } from 'react'
import { Globe } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { ShareTunnelStatusResponse, WorkflowPublishInfoResponse, WorkflowPublishStrategyInfo } from '../../services/api-types'
import PublishPopupBody from '../backup-publish/PublishPopupBody'

interface WorkflowPublishViewProps {
  workspacePath: string | null
  onStateLoaded?: (state: string) => void
}

const FALLBACK_SUPPORTED: WorkflowPublishStrategyInfo[] = [
  { id: 'netlify', label: 'Netlify', method: 'cli', description: 'netlify deploy --prod; default URL *.netlify.app.' },
  { id: 'vercel', label: 'Vercel', method: 'cli', description: 'vercel deploy --prod; default URL *.vercel.app.' },
  { id: 'cloudflare-pages', label: 'Cloudflare Pages', method: 'cli', description: 'wrangler pages deploy; default URL *.pages.dev.' },
  { id: 'github-pages', label: 'GitHub Pages', method: 'git', description: 'Push static files to the gh-pages branch.' },
  { id: 's3', label: 'S3 / object store', method: 'sync', description: 'aws s3 sync / rclone to a static bucket — the any-host catch-all.' }
]

const getPublishSummary = (info: WorkflowPublishInfoResponse | null): string => {
  if (!info?.config?.enabled) return 'Not published yet. Share the dashboard with /share in chat.'
  switch (info?.effective_state) {
    case 'published':
      return 'Published. Your site is up to date.'
    case 'publishing':
      return 'Publishing now.'
    case 'configured_not_verified':
      return 'Set up. Waiting for the first publish.'
    case 'failed':
      return 'Last publish failed.'
    default:
      return 'Waiting for publish status.'
  }
}

const formatTunnelExpiry = (value?: string): string => {
  if (!value) return ''
  const when = new Date(value)
  if (Number.isNaN(when.getTime())) return value
  return when.toLocaleString()
}

const InternetShareSection: React.FC = () => {
  const [status, setStatus] = useState<ShareTunnelStatusResponse | null>(null)

  useEffect(() => {
    let cancelled = false
    agentApi.getShareTunnelStatus()
      .then((resp) => { if (!cancelled) setStatus(resp) })
      // Non-admins get 403 from this endpoint; the section stays hidden.
      .catch(() => { if (!cancelled) setStatus(null) })
    return () => { cancelled = true }
  }, [])

  if (status === null) return null
  return (
    <section className="overflow-hidden rounded-md border border-border">
      <div className="flex items-start gap-3 px-4 py-4">
        <Globe className="mt-0.5 h-4 w-4 flex-shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">Internet share</div>
          {status.active ? (
            <>
              <a href={status.public_url} target="_blank" rel="noreferrer" className="block truncate text-sm text-blue-500 hover:underline">
                {status.public_url}
              </a>
              <p className="mt-1 text-xs text-amber-600 dark:text-amber-500">
                Temporary tunnel — exposes the whole server, not one report. Links die with it.
                {status.expires_at ? ` Expires ${formatTunnelExpiry(status.expires_at)}.` : ''}
              </p>
            </>
          ) : (
            <p className="mt-1 text-xs text-muted-foreground">
              No internet share active. Ask in chat to start a temporary one for off-machine report links.
            </p>
          )}
        </div>
      </div>
    </section>
  )
}

const WorkflowPublishView: React.FC<WorkflowPublishViewProps> = ({ workspacePath, onStateLoaded }) => {
  const loadInfo = useCallback(async () => {
    if (!workspacePath) throw new Error('No workflow is selected')
    return agentApi.getWorkflowPublish(workspacePath)
  }, [workspacePath])

  const loadAccessSecret = useCallback(async (secretName: string) => {
    if (!workspacePath) throw new Error('No workflow is selected')
    const resp = await agentApi.getWorkflowPublishSecret(workspacePath, secretName)
    return resp.value
  }, [workspacePath])

  return (
    <div className="space-y-4">
    <PublishPopupBody
      loadInfo={loadInfo}
      loadAccessSecret={loadAccessSecret}
      onStateLoaded={onStateLoaded}
      fallbackStrategies={FALLBACK_SUPPORTED}
      subtitle="Publish this automation's dashboards at a public URL"
      emptyDestinationsText="No hosts yet — share the dashboard with /share in chat."
      destinationsHelp="After every run refreshes a configured publication only when something changed."
      supportedHelp="Suggestions, not a limit — any static host works."
      defaultTargetLabel="dashboard"
      getSummary={getPublishSummary}
      headerAction={<GoalAfterRunToggle workspacePath={workspacePath} option="publish" />}
    />
    <InternetShareSection />
    </div>
  )
}

export default WorkflowPublishView

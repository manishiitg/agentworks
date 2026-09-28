import { useEffect, useMemo, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import {
  Loader2,
  AlertCircle,
  Bug,
  Search,
} from 'lucide-react'
import ConnectionIcon from './ConnectionIcon'
import { brandSlugFor } from './brandSlug'
import { GROUP_ORDER, descriptionFor, groupFor, statusIndicator } from './catalog'
import { useMCPStore } from '../../stores'
import { useAuthStore } from '../../stores/useAuthStore'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import MCPConfigPopup from '../MCPConfigPopup'
import { AskAIButton } from '../workflow/AskAIButton'

interface ConnectorsBrowserProps {
  // The embedded workflow panel uses tighter spacing for a narrow side panel.
  compact?: boolean
  workspacePath?: string | null
  /** Terminology for the workspace receiving the connection. */
  workspaceLabel?: string
  /** Name of the conversational agent that helps with setup. */
  assistantLabel?: string
  /** Optional product-owned chat delivery; workflows use their existing lane. */
  onAskAI?: (message: string) => void | Promise<void>
  /** Let an embedding scroll the complete page instead of only this list. */
  manageOwnScroll?: boolean
  /** Filter the list by this query instead of the internal search box --
   * for a host that renders one shared search box above several pickers. */
  query?: string
  /** Hide the internal search box (pairs with a host-provided `query`). */
  hideSearch?: boolean
  /** Hide the Connected shelf -- for a host that lists attached servers
   * itself and only uses this browser to connect new ones. */
  hideConnectedSection?: boolean
  /** Hide the builder banner -- for a host that renders its own shared
   * banner above this browser. */
  hideBanner?: boolean
}

export default function ConnectorsBrowser({
  compact = false,
  workspacePath,
  workspaceLabel = 'workflow',
  assistantLabel = 'builder',
  onAskAI,
  manageOwnScroll = true,
  query: externalQuery,
  hideSearch = false,
  hideConnectedSection = false,
  hideBanner = false,
}: ConnectorsBrowserProps) {
  const {
    toolList,
    isLoadingTools,
    toolsError,
    getServerGroups,
    refreshTools,
  } = useMCPStore(useShallow(state => ({
    toolList: state.toolList,
    isLoadingTools: state.isLoadingTools,
    toolsError: state.toolsError,
    getServerGroups: state.getServerGroups,
    refreshTools: state.refreshTools,
  })))

  // Refetch on mount rather than trusting whatever the store last held (app
  // boot, or the last time some other instance of this panel called
  // refreshTools). A chat-driven install_mcp_server call has no way to push
  // into this store directly, so a server installed since this panel was
  // last open would otherwise render as if it didn't exist.
  useEffect(() => {
    void refreshTools()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)
  const canManagePlatformMCP = useAuthStore(state =>
    state.user?.is_admin === true || (state.isMultiUserModeChecked && !state.isMultiUserMode),
  )
  // Everyone can reuse connected MCPs; only platform admins may change the
  // deployment-wide connection registry.
  const readOnly = !canWriteWorkflow || !canManagePlatformMCP
  const [internalQuery, setInternalQuery] = useState('')
  const query = externalQuery ?? internalQuery
  // Local to this instance -- deliberately independent of the store's global
  // showConfigEditor/showMCPDetails flags, which were owned by the (since
  // removed) top-menu MCP modal. Reusing a global flag here would let two
  // embedded browsers fight over one popup.
  const [showJsonConfig, setShowJsonConfig] = useState(false)

  const groups = getServerGroups()

  // A single list, connected first then alphabetical, so the services in
  // use stay at the top instead of scattering through it as tokens come
  // and go.
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    return Object.entries(groups)
      .filter(([name]) => {
        if (!q) return true
        return (
          name.toLowerCase().includes(q) ||
          descriptionFor(name).toLowerCase().includes(q)
        )
      })
      .sort(([aName, aTools], [bName, bTools]) => {
        const aOk = aTools[0]?.connection === 'connected' ? 0 : 1
        const bOk = bTools[0]?.connection === 'connected' ? 0 : 1
        return aOk - bOk || aName.localeCompare(bName)
      })
  }, [groups, query])

  const total = Object.keys(groups).length
  const cardPadding = compact ? 'p-3' : 'p-4'
  const gridGap = compact ? 'gap-2' : 'gap-3'

  const connectedVisible = visible.filter(([, tools]) => tools[0]?.connection === 'connected')
  const otherVisible = visible.filter(([, tools]) => tools[0]?.connection !== 'connected')

  // Raw-JSON escape hatch for admins, parked on the first list heading
  // rather than in its own banner action.
  const debugJsonButton = !readOnly && (
    <button
      type="button"
      onClick={() => setShowJsonConfig(true)}
      title="Debug: edit the raw shared MCP server JSON"
      aria-label="Debug: edit raw MCP JSON"
      className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground/50 transition-colors hover:bg-muted hover:text-muted-foreground"
    >
      <Bug className="h-3 w-3" />
    </button>
  )

  const renderCard = ([serverName, tools]: (typeof visible)[number]) => {
    const status = tools[0]?.status
    const connection = tools[0]?.connection

    return (
      <div
        key={serverName}
        className="flex flex-col rounded-xl border border-gray-200 bg-white transition-colors hover:border-gray-300 dark:border-gray-800 dark:bg-gray-900/60 dark:hover:border-gray-700"
      >
        <div className={`flex items-start gap-3 ${cardPadding}`}>
          <ConnectionIcon
            icon={brandSlugFor(serverName)}
            name={serverName}
            size="lg"
          />

          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              <span className="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">
                {serverName}
              </span>
              <span
                className={`h-2 w-2 shrink-0 rounded-full ${statusIndicator(connection, status).dot}`}
                title={statusIndicator(connection, status).title}
                aria-label={statusIndicator(connection, status).title}
              />
            </div>
            <p className="mt-0.5 line-clamp-2 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
              {descriptionFor(serverName)}
            </p>
          </div>

          <div className="flex shrink-0 flex-col items-center gap-1 self-center">
            <AskAIButton
              workspacePath={readOnly ? null : workspacePath ?? null}
              onAsk={onAskAI}
              iconOnly
              label={`Ask AI about ${serverName}`}
              message={connection === 'connected'
                ? `Help me with the existing ${JSON.stringify(serverName)} MCP connection in this ${workspaceLabel}. Check its current connection status and ${workspaceLabel} selection, then ask what I want to do with it.`
                : canManagePlatformMCP
                  ? `Help me connect ${JSON.stringify(serverName)} to this ${workspaceLabel}. It is already listed in the MCP catalog, so check its existing configuration and connection status first and reuse it. Before authorization, remind me that the account will be shared by every AgentWorks user and product. Guide me through the required authorization or secure credential setup, verify its tools, then add it to this ${workspaceLabel}. Do not ask me to paste secrets into chat.`
                  : `Explain that ${JSON.stringify(serverName)} is not yet connected to this AgentWorks platform, that only an administrator can connect the shared external account, and how I can select it for this ${workspaceLabel} after an administrator connects it. Do not attempt to change MCP configuration.`}
              className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-primary/10 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
            />
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className={manageOwnScroll ? 'flex h-full min-h-0 flex-col' : 'flex flex-col'}>
      {/* Search */}
      {!hideSearch && (
        <div className="relative shrink-0">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
          <input
            type="text"
            value={query}
            onChange={(e) => setInternalQuery(e.target.value)}
            placeholder="Search connectors"
            aria-label="Search connectors"
            className="w-full rounded-lg border border-gray-300 bg-white py-2.5 pl-10 pr-3 text-sm text-gray-900 placeholder-gray-400 transition-colors focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100"
          />
        </div>
      )}
      {!hideBanner && (
        <div className="mt-3 flex shrink-0 flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 p-3">
          <div className="min-w-0 flex-1 basis-48">
            <p className="text-sm font-semibold text-foreground">{"Can't find the app you need?"}</p>
            <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
              {canManagePlatformMCP
                ? `The ${assistantLabel} can connect it for you. New connections are shared with everyone.`
                : 'Only an admin can add new connections. Everything connected below is ready to use.'}
            </p>
          </div>
          <AskAIButton
            workspacePath={readOnly ? null : workspacePath ?? null}
            onAsk={onAskAI}
            label={canManagePlatformMCP ? `Ask ${assistantLabel} to connect` : "Why can't I add one?"}
            message={canManagePlatformMCP
              ? query.trim()
                ? `Help me add an MCP server for ${JSON.stringify(query.trim())} to this ${workspaceLabel}. Search the catalog and official provider documentation on the web, and help me connect it. Before authorization, remind me that the account will be shared by every AgentWorks user and product.`
                : `Help me add a shared platform MCP server to this ${workspaceLabel}. Ask which app or service I want, then search the catalog and official provider documentation and help me connect it. Before authorization, remind me that the account will be shared by every AgentWorks user and product.`
              : `Explain how shared platform MCP connections work in AgentWorks, why only an administrator can add or replace one, and how I can reuse an already-connected service in this ${workspaceLabel}. Do not attempt to change MCP configuration.`}
            className="inline-flex shrink-0 items-center justify-center gap-2 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          />
        </div>
      )}

      {/* Grid */}
      <div className={manageOwnScroll ? 'min-h-0 flex-1 overflow-y-auto pt-5' : 'pt-5'}>
        {isLoadingTools && toolList.length === 0 && (
          <div className="flex items-center gap-2 py-8 text-sm text-gray-500 dark:text-gray-400">
            <Loader2 className="h-4 w-4 animate-spin" />
            <span>Loading connectors...</span>
          </div>
        )}

        {toolsError && (
          <div className="flex items-center gap-2 py-3 text-sm text-red-500 dark:text-red-400">
            <AlertCircle className="h-4 w-4" />
            <span>Error: {toolsError}</span>
          </div>
        )}

        {!isLoadingTools && visible.length === 0 && !toolsError && (
          <p className="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            {total === 0
              // An empty catalog is a deployment fact: this server's MCP
              // config lists no connectors at all.
              ? 'No connectors are configured on this server. The deployment’s MCP server config lists none.'
              : `No connectors match "${query}". Ask in chat — it can search the web and connect one directly, even if it isn't in this list.`}
          </p>
        )}

        {!hideConnectedSection && connectedVisible.length > 0 && (
          <section className="mb-5 last:mb-0">
            <h4 className="mb-3 flex items-center justify-between border-b border-gray-200 pb-2 text-sm font-semibold text-gray-900 dark:border-gray-800 dark:text-gray-100">
              <span>Connected</span>
              {debugJsonButton}
            </h4>
            <div className={`grid grid-cols-1 ${gridGap} md:grid-cols-2`}>
              {connectedVisible.map(renderCard)}
            </div>
          </section>
        )}
        {otherVisible.length > 0 && (
          <section>
            <h4 className="mb-3 flex items-center justify-between border-b border-gray-200 pb-2 text-sm font-semibold text-gray-900 dark:border-gray-800 dark:text-gray-100">
              <span>Others</span>
              {(hideConnectedSection || connectedVisible.length === 0) && debugJsonButton}
            </h4>
            {GROUP_ORDER.map(({ id, label }) => {
              const entries = otherVisible.filter(([name]) => groupFor(name) === id)
              if (entries.length === 0) return null
              return (
                <div key={id} className="mb-5 last:mb-0">
                  <p className="mb-2 text-xs font-semibold text-muted-foreground">{label}</p>
                  <div className={`grid grid-cols-1 ${gridGap} md:grid-cols-2`}>
                    {entries.map(renderCard)}
                  </div>
                </div>
              )
            })}
          </section>
        )}
      </div>

      {showJsonConfig && (
        <MCPConfigPopup
          initialView="json"
          onConfigChange={() => refreshTools()}
          onClose={() => setShowJsonConfig(false)}
        />
      )}
    </div>
  )
}

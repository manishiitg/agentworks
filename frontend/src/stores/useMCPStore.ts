import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { ToolDefinition, StoreActions } from './types'
import { agentApi } from '../services/api'
import { mcpConfigApi } from '../services/mcpConfigApi'
import type { ServerLogEntry } from '../services/mcpConfigApi'

// Reset invalidates outstanding requests when the authenticated account changes.
let mcpGeneration = 0

interface MCPState extends StoreActions {
  // Server and tool data
  toolList: ToolDefinition[]
  enabledServers: string[]
  enabledTools: string[]

  // Mode-specific server selections (multi-agent vs workflow)
  chatSelectedServers: string[]
  workflowSelectedServers: string[]
  
  // UI state
  expandedServers: Set<string>
  selectedTool: {serverName: string, toolName: string} | null
  toolDetails: Record<string, ToolDefinition>
  loadingToolDetails: Set<string>
  
  // Modal states
  showMCPDetails: boolean
  showRegistryModal: boolean
  showConfigEditor: boolean
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  showApiTester: { serverName: string; toolName: string; toolDetail?: any } | null
  
  // Server logs
  serverLogs: Record<string, ServerLogEntry[]>

  // Loading states
  isLoadingTools: boolean
  toolsError: string | null

  // Actions
  setEnabledServers: (servers: string[]) => void
  refreshTools: () => Promise<void>

  // Mode-specific server actions
  setChatSelectedServers: (servers: string[]) => void
  setWorkflowSelectedServers: (servers: string[]) => void
  getServersForMode: (mode: 'multi-agent' | 'workflow') => string[]
  
  // Tool detail actions
  setExpandedServers: (servers: Set<string>) => void
  toggleExpandedServer: (server: string) => void
  setSelectedTool: (tool: {serverName: string, toolName: string} | null) => void
  loadToolDetails: (serverName: string) => Promise<void>
  
  // Modal actions
  setShowMCPDetails: (show: boolean) => void
  setShowRegistryModal: (show: boolean) => void
  setShowConfigEditor: (show: boolean) => void
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  setShowApiTester: (value: { serverName: string; toolName: string; toolDetail?: any } | null) => void
  
  // Log actions
  fetchServerLogs: (serverName?: string) => Promise<void>

  // Helper methods
  getAvailableServers: () => string[]
  getServerGroups: () => Record<string, ToolDefinition[]>
  isServerEnabled: (server: string) => boolean
}

export const useMCPStore = create<MCPState>()(
    persist(
      (set, get) => ({
        // Initial state
        toolList: [],
        enabledServers: [],
        enabledTools: [],
        // Mode-specific server selections
        chatSelectedServers: [],
        workflowSelectedServers: [],
        expandedServers: new Set(),
        selectedTool: null,
        toolDetails: {},
        loadingToolDetails: new Set(),
        showMCPDetails: false,
        showRegistryModal: false,
        showConfigEditor: false,
        showApiTester: null,
        serverLogs: {},
        isLoadingTools: true,
        toolsError: null,

        // Actions
        setEnabledServers: (servers) => {
          set({ enabledServers: servers })
        },

        // Mode-specific server actions
        setChatSelectedServers: (servers) => {
          set({ chatSelectedServers: servers })
        },

        setWorkflowSelectedServers: (servers) => {
          set({ workflowSelectedServers: servers })
        },

        getServersForMode: (mode) => {
          const state = get()
          return mode === 'workflow'
            ? state.workflowSelectedServers
            : state.chatSelectedServers
        },

        refreshTools: async () => {
          const generation = mcpGeneration
          set({ isLoadingTools: true, toolsError: null })

          try {
            const toolList = await agentApi.getTools() as ToolDefinition[]
            if (generation !== mcpGeneration) return

            // Servers the user has connected. Reachability (status === 'ok') is a
            // separate question — a connected server that is down stays in this list.
            const availableServers = [...new Set(toolList.filter((tool: ToolDefinition) => tool.connection === 'connected').map((tool: ToolDefinition) => tool.server).filter((server): server is string => typeof server === 'string'))]

            // Filter persisted enabledServers to only include servers that still exist
            // This removes servers that were deleted from the config
            const currentEnabledServers = get().enabledServers
            const filteredEnabledServers = currentEnabledServers.filter(server => availableServers.includes(server))

            const filterSelectedServers = (servers: string[]) => servers.filter(server =>
              server === "NO_SERVERS" || server.startsWith("vault_") || availableServers.includes(server)
            )
            const filteredChatSelectedServers = filterSelectedServers(get().chatSelectedServers)
            const filteredWorkflowSelectedServers = filterSelectedServers(get().workflowSelectedServers)

            // Filter persisted toolDetails to only include servers that still exist
            const currentToolDetails = get().toolDetails
            const filteredToolDetails: Record<string, ToolDefinition> = {}
            for (const server of Object.keys(currentToolDetails)) {
              if (availableServers.includes(server)) {
                filteredToolDetails[server] = currentToolDetails[server]
              }
            }

            // Filter expandedServers to only include servers that still exist
            const currentExpandedServers = get().expandedServers
            const filteredExpandedServers = new Set<string>()
            currentExpandedServers.forEach(server => {
              if (availableServers.includes(server)) {
                filteredExpandedServers.add(server)
              }
            })

            set({
              toolList,
              isLoadingTools: false,
              // No auto-enable: connecting a server is an explicit user action,
              // so an empty list stays empty.
              enabledServers: filteredEnabledServers,
              chatSelectedServers: filteredChatSelectedServers,
              workflowSelectedServers: filteredWorkflowSelectedServers,
              // Update toolDetails to remove deleted servers
              toolDetails: filteredToolDetails,
              // Update expandedServers to remove deleted servers
              expandedServers: filteredExpandedServers
            })

            // If any server is still being discovered, poll again after 2s.
            // Idle/not_loaded catalog entries must not trigger polling.
            const hasLoadingServers = toolList.some((t: ToolDefinition) => t.status === 'loading')
            if (hasLoadingServers) {
              setTimeout(() => { if (generation === mcpGeneration) void get().refreshTools() }, 2000)
            }
          } catch (error) {
            if (generation !== mcpGeneration) return
            set({
              toolsError: error instanceof Error ? error.message : 'Failed to load tools',
              isLoadingTools: false
            })
          }
        },

        // Tool detail actions
        setExpandedServers: (servers) => {
          set({ expandedServers: servers })
        },

        toggleExpandedServer: (server) => {
          set((state) => {
            const newExpanded = new Set(state.expandedServers)
            if (newExpanded.has(server)) {
              newExpanded.delete(server)
            } else {
              newExpanded.add(server)
            }
            return { expandedServers: newExpanded }
          })
        },

        setSelectedTool: (tool) => {
          set({ selectedTool: tool })
        },

        loadToolDetails: async (serverName) => {
          const generation = mcpGeneration
          const state = get()
          if (state.toolDetails[serverName] || state.loadingToolDetails.has(serverName)) {
            return // Already loaded or loading
          }

          set((state) => ({
            loadingToolDetails: new Set([...state.loadingToolDetails, serverName])
          }))

          try {
            const toolDetail = await agentApi.getToolDetail(serverName)
            if (generation !== mcpGeneration) return
            set((state) => ({
              toolDetails: {
                ...state.toolDetails,
                [serverName]: toolDetail
              },
              loadingToolDetails: new Set([...state.loadingToolDetails].filter(s => s !== serverName))
            }))
          } catch (error) {
            if (generation !== mcpGeneration) return
            console.error(`Failed to load tool details for ${serverName}:`, error)
            set((state) => ({
              loadingToolDetails: new Set([...state.loadingToolDetails].filter(s => s !== serverName))
            }))
          }
        },

        // Modal actions
        setShowMCPDetails: (show) => {
          set({ showMCPDetails: show })
        },

        setShowRegistryModal: (show) => {
          set({ showRegistryModal: show })
        },

        setShowConfigEditor: (show) => {
          set({ showConfigEditor: show })
        },

        setShowApiTester: (value) => {
          set({ showApiTester: value })
        },

        // Log actions
        fetchServerLogs: async (serverName?: string) => {
          const generation = mcpGeneration
          try {
            const response = await mcpConfigApi.getServerLogs(serverName)
            if (generation !== mcpGeneration) return
            set((state) => ({
              serverLogs: { ...state.serverLogs, ...response.logs }
            }))
          } catch (error) {
            console.error('Failed to fetch server logs:', error)
          }
        },

        // Helper methods
        getAvailableServers: () => {
          const state = get()
          return [...new Set(state.toolList.filter((tool: ToolDefinition) => tool.connection === 'connected').map((tool: ToolDefinition) => tool.server).filter((server): server is string => typeof server === 'string'))]
        },

        getServerGroups: () => {
          const state = get()
          const groups: Record<string, ToolDefinition[]> = {}
          state.toolList.forEach(tool => {
            if (tool.server) {
              if (!groups[tool.server]) {
                groups[tool.server] = []
              }
              groups[tool.server].push(tool)
            }
          })
          return groups
        },

        isServerEnabled: (server) => {
          const state = get()
          return state.enabledServers.includes(server)
        },

        // Generic actions
        reset: () => {
          mcpGeneration += 1
          set({
            toolList: [],
            enabledServers: [],
            enabledTools: [],
            chatSelectedServers: [],
            workflowSelectedServers: [],
            expandedServers: new Set(),
            selectedTool: null,
            toolDetails: {},
            loadingToolDetails: new Set(),
            showMCPDetails: false,
            showRegistryModal: false,
            showConfigEditor: false,
            showApiTester: null,
            serverLogs: {},
            isLoadingTools: true,
            toolsError: null
          })
        },

        setLoading: (loading) => {
          set({ isLoadingTools: loading })
        },

        setError: (error) => {
          set({ toolsError: error })
        }
      }),
      {
        name: 'mcp-store',
        version: 1,
        // Drop unowned pre-private MCP preferences and tool metadata.
        migrate: () => ({}),
        partialize: (state) => ({
          // Only persist user preferences, not temporary state
          enabledServers: state.enabledServers,
          chatSelectedServers: state.chatSelectedServers,
          workflowSelectedServers: state.workflowSelectedServers,
          expandedServers: Array.from(state.expandedServers), // Convert Set to Array for persistence
        }),
        onRehydrateStorage: () => (state) => {
          // Convert expandedServers array back to Set
          if (state && Array.isArray(state.expandedServers)) {
            state.expandedServers = new Set(state.expandedServers)
          }
          // Ensure loading state is false after rehydration if we have tools
          if (state && state.toolList && state.toolList.length > 0) {
            state.isLoadingTools = false
          }
          // One-time migration from the pre-mode-specific persisted selection.
          if (state) {
            const migratedState = state as MCPState & { selectedServers?: string[] }
            const legacyServers = migratedState.selectedServers || []
            const hasLegacyServers = legacyServers.length > 0
            const hasChatServers = state.chatSelectedServers && state.chatSelectedServers.length > 0
            const hasWorkflowServers = state.workflowSelectedServers && state.workflowSelectedServers.length > 0

            if (hasLegacyServers && !hasChatServers) {
              state.chatSelectedServers = [...legacyServers]
            }
            if (hasLegacyServers && !hasWorkflowServers) {
              state.workflowSelectedServers = [...legacyServers]
            }
            delete migratedState.selectedServers
          }
        }
      }
    )
)

import React, { useCallback, useEffect, useMemo } from 'react';
import { Checkbox } from './ui/checkbox';
import { Check, Loader2 } from 'lucide-react';
import { useToolSelectionStore } from '../stores/useToolSelectionStore';
import { useMCPStore } from '../stores/useMCPStore';
import { isSelectedServer, serverNamesMatch, toolBelongsToServer, hasServerTool, toggleServerSelection } from '../utils/mcpServerAlias';
import ConnectionIcon from './connectors/ConnectionIcon';
import { brandSlugFor } from './connectors/brandSlug';

interface ToolSelectionSectionProps {
  availableServers: string[];
  selectedServers: string[];
  selectedTools: string[]; // Array of "server:tool"
  onServerChange: (servers: string[]) => void;
  onToolChange: (tools: string[]) => void;
  /** Save a checkbox's server and tool changes as one selection update. */
  onSelectionChange?: (servers: string[], tools: string[]) => void;
  stepId?: string; // Optional step ID for debugging
  agentMode: string; // Add agentMode prop
  /** Lets the server list use an embedded side panel's remaining vertical space. */
  fillAvailableHeight?: boolean;
  /** Suppress the "MCP Server Selection" title + description -- for a host
   * that already shows its own equivalent section header above this. */
  hideHeader?: boolean;
  /** Filter the server list by name -- for a host that renders one shared
   * search box above several pickers. */
  query?: string;
  /** Let an embedding scroll the complete page instead of only this list. */
  manageOwnScroll?: boolean;
  /** Keep the selection visible while preventing read-only mutations. */
  disabled?: boolean;
  /** Hide per-tool expansion (tool counts, mode picker, tool list) so the
   * section works as a server-level checklist. For hosts whose runtime only
   * consumes server selection — per-tool picks would have no effect there,
   * so the UI must not offer them. */
  hideToolDetails?: boolean;
}

export const ToolSelectionSection: React.FC<ToolSelectionSectionProps> = ({
  availableServers,
  selectedServers,
  selectedTools,
  onServerChange,
  onToolChange,
  onSelectionChange,
  stepId,
  fillAvailableHeight = false,
  hideHeader = false,
  query = '',
  manageOwnScroll = true,
  disabled = false,
  hideToolDetails = false,
}) => {
  // Generate instance ID from stepId or use a default
  const instanceId = useMemo(() => stepId || `preset-${Date.now()}`, [stepId]);
  
  // Get store state and actions
  // Select instance directly - use a stable selector to avoid infinite loop
  const rawInstance = useToolSelectionStore((state) => {
    const instance = state.instances[instanceId];
    // Return the instance directly (Zustand will handle memoization)
    return instance;
  });
  
  // Get actions directly from store (not as selectors to avoid re-renders)
  const storeActions = useMemo(() => ({
    syncServerToolMode: useToolSelectionStore.getState().syncServerToolMode,
    loadServerTools: useToolSelectionStore.getState().loadServerTools,
    getServerTools: useToolSelectionStore.getState().getServerTools,
    isServerLoading: useToolSelectionStore.getState().isServerLoading,
    toggleExpandedServer: useToolSelectionStore.getState().toggleExpandedServer,
    updateServerToolMode: useToolSelectionStore.getState().updateServerToolMode,
    removeInstance: useToolSelectionStore.getState().removeInstance,
    getInstanceState: useToolSelectionStore.getState().getInstanceState,
  }), []);
  
  // Get MCP server connection status
  const mcpToolList = useMCPStore((state) => state.toolList);
  const isLoadingConnections = useMCPStore((state) => state.isLoadingTools);
  const serverStatusMap = useMemo(() => {
    const map: Record<string, 'ok' | 'error' | 'loading' | 'unknown'> = {};
    mcpToolList.forEach(tool => {
      if (tool.server) {
        const current = map[tool.server];
        const toolStatus = (tool.status as string) || 'unknown';
        // ok wins over error wins over loading wins over unknown
        if (!current || (toolStatus === 'ok') || (current !== 'ok' && toolStatus === 'error')) {
          map[tool.server] = toolStatus as 'ok' | 'error' | 'loading' | 'unknown';
        }
      }
    });
    return map;
  }, [mcpToolList]);

  // Use fallback instance to avoid null checks everywhere
  // Create a stable default instance that won't change
  const defaultInstance = useMemo(() => ({
    expandedServers: new Set<string>(),
    serverToolMode: {} as Record<string, 'all' | 'specific'>,
    loadingServers: new Set<string>(),
  }), []);
  
  const instance = rawInstance || defaultInstance;

  // Initialize instance if it doesn't exist
  useEffect(() => {
    if (!rawInstance) {
      storeActions.getInstanceState(instanceId);
    }
  }, [rawInstance, instanceId, storeActions]);
  
  // Sync mode when selectedServers or selectedTools change
  useEffect(() => {
    if (rawInstance) {
      storeActions.syncServerToolMode(instanceId, selectedServers, selectedTools);
    }
  }, [rawInstance, instanceId, selectedServers, selectedTools, storeActions]);
  
  // Auto-load tools for servers in specific mode that haven't been loaded yet
  useEffect(() => {
    if (!rawInstance) return;
    
    selectedServers.forEach(serverName => {
      // Calculate mode the same way as in render (check store mode first, then calculated)
      const hasAllToolsMarker = selectedTools.includes(`${serverName}:*`);
      const serverSpecificTools = selectedTools.filter(t => 
        t.startsWith(`${serverName}:`) && !t.endsWith(':*')
      );
      const calculatedMode = hasAllToolsMarker ? 'all' : (serverSpecificTools.length > 0 ? 'specific' : 'all');
      // Access serverToolMode from rawInstance to avoid dependency on instance object
      const toolMode = rawInstance.serverToolMode[serverName] || calculatedMode;
      
      // If in specific mode and tools haven't been loaded, load them
      if (toolMode === 'specific') {
        const toolsFromStore = storeActions.getServerTools(serverName);
        const hasLoadedTools = toolsFromStore !== undefined;
        const isLoading = storeActions.isServerLoading(instanceId, serverName);
        
        if (!hasLoadedTools && !isLoading) {
          console.log('[ToolSelection] Auto-loading tools for server in specific mode:', serverName);
          // Trigger load
          const setLoadingServer = useToolSelectionStore.getState().setLoadingServer;
          setLoadingServer(instanceId, serverName, true);
          storeActions.loadServerTools(serverName)
            .then(() => {
              console.log('[ToolSelection] Tools loaded successfully for:', serverName);
              setLoadingServer(instanceId, serverName, false);
            })
            .catch(err => {
              console.error('[ToolSelection] Failed to load tools:', serverName, err);
              setLoadingServer(instanceId, serverName, false);
            });
        }
      }
    });
  }, [rawInstance, instanceId, selectedServers, selectedTools, storeActions]);
  
  // Cleanup on unmount
  useEffect(() => {
    return () => {
      storeActions.removeInstance(instanceId);
    };
  }, [instanceId, storeActions]);
  
  // Auto-expand server when selected
  const expandServer = useCallback((serverName: string) => {
    if (!instance.expandedServers.has(serverName)) {
      storeActions.toggleExpandedServer(instanceId, serverName);
    }
    // Load tools if not already loaded
    const tools = storeActions.getServerTools(serverName);
    if (!tools) {
      // Set loading state before loading
      const setLoadingServer = useToolSelectionStore.getState().setLoadingServer;
      setLoadingServer(instanceId, serverName, true);
      storeActions.loadServerTools(serverName)
        .then(() => {
          setLoadingServer(instanceId, serverName, false);
        })
        .catch(err => {
          console.error('[ToolSelection] Failed to load tools:', serverName, err);
          setLoadingServer(instanceId, serverName, false);
        });
    }
  }, [instanceId, instance.expandedServers, storeActions]);

  // Handle server checkbox
  const handleServerToggle = useCallback((serverName: string) => {
    if (disabled) return;
    const wasSelected = isSelectedServer(selectedServers, serverName);
    const { servers, tools } = toggleServerSelection(serverName, selectedServers, selectedTools);
    if (onSelectionChange) onSelectionChange(servers, tools);
    else {
      onServerChange(servers);
      onToolChange(tools);
    }

    if (!wasSelected) {
      // Always expand when server is selected so user can choose tool mode
      expandServer(serverName);
    }
  }, [disabled, selectedServers, selectedTools, onServerChange, onToolChange, onSelectionChange, expandServer]);

  // Handle switching between "all tools" and "specific tools" for a server
  const handleServerToolModeChange = useCallback((serverName: string, mode: 'all' | 'specific') => {
    if (disabled) return;
    console.log('[ToolSelection] Mode change:', serverName, '->', mode);
    
    // Update mode in store immediately
    storeActions.updateServerToolMode(instanceId, serverName, mode);
    
    if (mode === 'all') {
      // Set special marker "server:*" to indicate "all tools" mode
      // Remove all specific tools for this server
      const newTools = selectedTools.filter(t => !t.startsWith(`${serverName}:`));
      newTools.push(`${serverName}:*`);
      console.log('[ToolSelection] Setting all tools mode, newTools:', newTools);
      onToolChange(newTools);
    } else {
      // Remove the special marker and switch to specific mode
      // Keep any existing specific tools for this server
      const newTools = selectedTools.filter(t => t !== `${serverName}:*`);
      console.log('[ToolSelection] Setting specific tools mode, newTools:', newTools);
      
      onToolChange(newTools);
      // Load tools for this server when switching to specific mode (force reload)
      const setLoadingServer = useToolSelectionStore.getState().setLoadingServer;
      setLoadingServer(instanceId, serverName, true);
      storeActions.loadServerTools(serverName, true)
        .then(() => {
          setLoadingServer(instanceId, serverName, false);
        })
        .catch(err => {
          console.error('[ToolSelection] Failed to load tools:', serverName, err);
          setLoadingServer(instanceId, serverName, false);
        });
      // Expand the server when switching to specific mode so user can see tools
      expandServer(serverName);
    }
  }, [disabled, instanceId, selectedTools, onToolChange, expandServer, storeActions]);

  // Handle tool checkbox
  const handleToolToggle = useCallback((serverName: string, toolName: string) => {
    if (disabled) return;
    const fullName = `${serverName}:${toolName}`;
    const isSelected = selectedTools.includes(fullName);
    
    if (isSelected) {
      onToolChange(selectedTools.filter(t => t !== fullName));
    } else {
      onToolChange([...selectedTools, fullName]);
    }
  }, [disabled, selectedTools, onToolChange]);

  // Handle "Select all tools" for a server
  const handleSelectAllServerTools = useCallback((serverName: string) => {
    if (disabled) return;
    const serverTools = storeActions.getServerTools(serverName) || [];
    if (!Array.isArray(serverTools) || serverTools.length === 0) return;
    
    const serverToolNames = serverTools.map(t => `${serverName}:${t.name}`);
    
    const allSelected = serverToolNames.every(t => selectedTools.includes(t));
    
    if (allSelected) {
      // Deselect all
      const newTools = selectedTools.filter(t => !t.startsWith(`${serverName}:`));
      onToolChange(newTools);
    } else {
      // Select all
      const newTools = [...selectedTools];
      serverToolNames.forEach(t => {
        if (!newTools.includes(t)) {
          newTools.push(t);
        }
      });
      onToolChange(newTools);
    }
  }, [disabled, storeActions, selectedTools, onToolChange]);

  // Check if all tools from a server are selected
  const areAllServerToolsSelected = useCallback((serverName: string) => {
    // Check if in "all tools" mode first
    if (selectedTools.some(t => t.endsWith(':*') && toolBelongsToServer(t, serverName))) {
      return true;
    }

    const serverTools = storeActions.getServerTools(serverName) || [];
    if (!Array.isArray(serverTools) || serverTools.length === 0) return false;

    // Filter out "*" marker when counting specific tools
    const specificTools = selectedTools.filter(t =>
      toolBelongsToServer(t, serverName) && !t.endsWith(':*')
    );

    return specificTools.length > 0 && serverTools.every(t => hasServerTool(selectedTools, serverName, t.name));
  }, [storeActions, selectedTools]);

  return (
    <div className={fillAvailableHeight ? 'flex h-full min-h-0 flex-col gap-3' : 'space-y-3'}>
      {!hideHeader && (
        <>
          <label className="block shrink-0 text-sm font-medium text-gray-900 dark:text-gray-100 mb-2">
            MCP Server Selection
          </label>

          <div className="shrink-0 text-xs text-gray-500 dark:text-gray-400 mb-2">
            Select servers and choose whether to use all tools or select specific tools for each server.
          </div>
        </>
      )}

      {/* Server and Tool List -- no outer border here: each card already has
          its own, and a wrapping frame right up against a full-width
          expanded card reads as a redundant double border. */}
      <div className={manageOwnScroll ? `overflow-y-auto ${fillAvailableHeight ? 'min-h-0 flex-1' : 'max-h-96'}` : ''}>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {availableServers
          .filter(serverName => serverName !== 'mcp')
          .filter(serverName => !query.trim() || serverName.toLowerCase().includes(query.trim().toLowerCase()))
          .sort((a, b) => {
            const aSelected = isSelectedServer(selectedServers, a);
            const bSelected = isSelectedServer(selectedServers, b);
            if (aSelected && !bSelected) return -1;
            if (!aSelected && bSelected) return 1;
            return a.localeCompare(b);
          })
          .map((serverName) => {
          const isExpanded = instance.expandedServers.has(serverName);
          const isLoading = storeActions.isServerLoading(instanceId, serverName);
          const isServerSelected = isSelectedServer(selectedServers, serverName);
          // Check if tools have been loaded (undefined = not loaded yet, array = loaded)
          const toolsFromStore = storeActions.getServerTools(serverName);
          const hasLoadedTools = toolsFromStore !== undefined;
          const serverTools = hasLoadedTools ? toolsFromStore : [];
          const allToolsSelected = areAllServerToolsSelected(serverName);

          // Calculate mode from selectedTools if not in serverToolMode (fallback for initial render)
          const hasAllToolsMarker = selectedTools.some(t => t.endsWith(':*') && toolBelongsToServer(t, serverName));
          const serverSpecificTools = selectedTools.filter(t =>
            toolBelongsToServer(t, serverName) && !t.endsWith(':*')
          );
          const calculatedMode = hasAllToolsMarker ? 'all' : (serverSpecificTools.length > 0 ? 'specific' : 'all');
          const toolMode = instance.serverToolMode[serverName] || calculatedMode;
          const isServerToolsArray = Array.isArray(serverTools);

          const connectionStatus = serverStatusMap[serverName];
          const isConnected = mcpToolList.some(tool =>
            tool.server && serverNamesMatch(tool.server, serverName) && tool.connection === 'connected'
          );
          const statusDotClass =
            !isConnected ? (isLoadingConnections ? 'bg-yellow-400' : 'bg-gray-400') :
            connectionStatus === 'ok' ? 'bg-green-500' :
            connectionStatus === 'error' ? 'bg-red-500' :
            connectionStatus === 'loading' ? 'bg-yellow-400' :
            'bg-green-500';
          const statusTitle =
            !isConnected ? (isLoadingConnections ? 'Checking connection...' : 'Disconnected') :
            connectionStatus === 'ok' ? 'Connected' :
            connectionStatus === 'error' ? 'Error' :
            connectionStatus === 'loading' ? 'Connecting...' :
            'Connected';

          const isExpandedCard = !hideToolDetails && isExpanded && isServerSelected;

          return (
            <div
              key={serverName}
              className={`rounded-md border border-gray-200 dark:border-gray-700 ${isExpandedCard ? 'sm:col-span-2' : ''}`}
            >
              {/* Server Row */}
              <div className="flex flex-col rounded-md p-2.5 hover:bg-gray-100 dark:hover:bg-gray-700">
                <div className="flex items-center gap-2">
                <Checkbox
                  id={`server-${serverName}`}
                  checked={isServerSelected}
                  onCheckedChange={() => handleServerToggle(serverName)}
                  disabled={disabled}
                />

                {/* Connector icon with a small connection-status badge on its corner */}
                <span className="relative flex-shrink-0">
                  <ConnectionIcon icon={brandSlugFor(serverName)} name={serverName} size="xs" />
                  <span
                    className={`absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border border-white dark:border-gray-900 ${statusDotClass}`}
                    title={statusTitle}
                  />
                </span>

                <label
                  htmlFor={`server-${serverName}`}
                  className="text-sm font-medium text-gray-900 dark:text-gray-100 cursor-pointer flex-1 select-none flex items-center gap-1.5 min-w-0"
                  onClick={(e) => {
                    // Only expand if server is selected and not already expanded
                    if (!hideToolDetails && isServerSelected && !isExpanded) {
                      e.stopPropagation();
                      expandServer(serverName);
                    }
                  }}
                >
                  <span className="truncate">{serverName}</span>
                  {isServerSelected && (
                    <span className="shrink-0 text-xs text-blue-700 dark:text-blue-300">Configured</span>
                  )}
                  {isServerSelected && !isConnected && !isLoadingConnections && (
                    <span className="shrink-0 rounded bg-amber-100 px-1.5 text-xs font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-200" title="Selected but not connected: its tools won't work until it is connected.">Needs connecting</span>
                  )}
                  {!hideToolDetails && isServerSelected && isServerToolsArray && serverTools.length > 0 && (
                    <span className="ml-1 text-xs text-gray-500 dark:text-gray-400 shrink-0">
                      ({toolMode === 'all' ? 'all tools' : `${selectedTools.filter(t => t.startsWith(`${serverName}:`) && !t.endsWith(':*')).length}/${serverTools.length} tools`})
                    </span>
                  )}
                </label>
                </div>
              </div>

              {/* Tool Mode Selection and Tool List (when expanded) */}
              {!hideToolDetails && isExpanded && isServerSelected && (
                <div className="pl-10 pr-3 pb-3 space-y-3">
                  {/* Tool Mode Selection */}
                  <div className="flex items-center space-x-4">
                    <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
                      Tool selection:
                    </label>
                    <button
                      type="button"
                      onClick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        console.log('[ToolSelection] All tools button clicked:', serverName);
                        handleServerToolModeChange(serverName, 'all');
                      }}
                      disabled={disabled}
                      title={disabled ? 'Read-only access' : undefined}
                      className={`flex items-center space-x-1.5 px-2 py-1 rounded border transition-colors ${
                        toolMode === 'all'
                          ? 'bg-blue-50 dark:bg-blue-900/30 border-blue-300 dark:border-blue-700 text-blue-700 dark:text-blue-300'
                          : 'bg-gray-50 dark:bg-gray-800 border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'
                      }`}
                    >
                      <div className={`w-3.5 h-3.5 rounded border-2 flex items-center justify-center flex-shrink-0 ${
                        toolMode === 'all'
                          ? 'border-blue-600 dark:border-blue-400 bg-blue-600 dark:bg-blue-400'
                          : 'border-gray-400 dark:border-gray-500'
                      }`}>
                        {toolMode === 'all' && (
                          <Check className="w-2.5 h-2.5 text-white" />
                        )}
                      </div>
                      <span className="text-xs font-medium whitespace-nowrap">Use all tools</span>
                    </button>
                    <button
                      type="button"
                      onClick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        console.log('[ToolSelection] Specific tools button clicked:', serverName);
                        handleServerToolModeChange(serverName, 'specific');
                      }}
                      disabled={disabled}
                      title={disabled ? 'Read-only access' : undefined}
                      className={`flex items-center space-x-1.5 px-2 py-1 rounded border transition-colors ${
                        toolMode === 'specific'
                          ? 'bg-blue-50 dark:bg-blue-900/30 border-blue-300 dark:border-blue-700 text-blue-700 dark:text-blue-300'
                          : 'bg-gray-50 dark:bg-gray-800 border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'
                      }`}
                    >
                      <div className={`w-3.5 h-3.5 rounded border-2 flex items-center justify-center flex-shrink-0 ${
                        toolMode === 'specific'
                          ? 'border-blue-600 dark:border-blue-400 bg-blue-600 dark:bg-blue-400'
                          : 'border-gray-400 dark:border-gray-500'
                      }`}>
                        {toolMode === 'specific' && (
                          <Check className="w-2.5 h-2.5 text-white" />
                        )}
                      </div>
                      <span className="text-xs font-medium whitespace-nowrap">Select specific tools</span>
                    </button>
                  </div>

                  {/* Tool List (only when specific mode is selected) */}
                  {toolMode === 'specific' && (
                    <div className="space-y-2">
                      {isLoading || !hasLoadedTools ? (
                        <div className="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400 py-2">
                          <Loader2 className="w-4 h-4 animate-spin" />
                          Loading tools...
                        </div>
                      ) : serverTools.length > 0 ? (
                        <>
                          {/* Select All Tools Button */}
                          <button
                            type="button"
                            onClick={() => handleSelectAllServerTools(serverName)}
                            disabled={disabled}
                            title={disabled ? 'Read-only access' : undefined}
                            className="text-xs text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 flex items-center gap-1"
                          >
                            {allToolsSelected ? (
                              <>
                                <Check className="w-3 h-3" />
                                Deselect all
                              </>
                            ) : (
                              <>Select all tools</>
                            )}
                          </button>
                          
                          {serverTools.map((tool) => {
                            const fullName = `${serverName}:${tool.name}`;
                            const isToolSelected = selectedTools.includes(fullName);
                            
                            return (
                              <div 
                                key={tool.name} 
                                className="flex items-start space-x-2"
                              >
                                <Checkbox
                                  id={`tool-${fullName}`}
                                  checked={isToolSelected}
                                  onCheckedChange={() => handleToolToggle(serverName, tool.name)}
                                  disabled={disabled}
                                  className="mt-1"
                                />
                                <label
                                  htmlFor={`tool-${fullName}`}
                                  className="text-sm cursor-pointer flex-1 select-none"
                                >
                                  <div className="font-medium text-gray-900 dark:text-gray-100">{tool.name}</div>
                                  {tool.description && (
                                    <div className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                                      {tool.description}
                                    </div>
                                  )}
                                </label>
                              </div>
                            );
                          })}
                        </>
                      ) : (
                        <div className="text-sm text-gray-500 dark:text-gray-400 py-2">
                          No tools available for this server
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>
          );
        })}
        </div>
      </div>

    </div>
  );
};

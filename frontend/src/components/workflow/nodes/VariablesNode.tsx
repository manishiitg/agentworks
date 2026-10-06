import { memo, useCallback, type MouseEvent } from 'react'
import { Handle, Position } from '@xyflow/react'
import { Variable, Layers, CheckCircle, Circle, Play, Check } from 'lucide-react'
import type { VariablesManifest, VariableGroup } from '../../../services/api-types'
import { useWorkflowStore } from '../../../stores/useWorkflowStore'

export interface VariablesNodeData extends Record<string, unknown> {
  manifest: VariablesManifest | null
  relayMode?: boolean
  onOpenSidebar?: () => void
  isLoading?: boolean
}

interface VariablesNodeProps {
  data: VariablesNodeData
  selected?: boolean
}

// Get enabled groups from manifest
const getEnabledGroups = (manifest: VariablesManifest | null): VariableGroup[] => {
  if (!manifest) return []
  if (!manifest.groups || manifest.groups.length === 0) {
    // Single group mode - create virtual group from variables
    if (!manifest.variables || !Array.isArray(manifest.variables)) return []
    const values: Record<string, string> = {}
    manifest.variables.forEach(v => {
      values[v.name] = v.value || ''
    })
    return [{
      name: 'group-1',
      values,
      enabled: true
    }]
  }
  return manifest.groups.filter(g => g.enabled)
}

const getGroupValues = (group: VariableGroup): Record<string, string> => {
  return group.values && typeof group.values === 'object' ? group.values : {}
}

// Check if manifest has multiple groups
const hasMultipleGroups = (manifest: VariablesManifest | null): boolean => {
  return !!manifest?.groups && manifest.groups.length > 1
}

export const VariablesNode = memo(({ data, selected }: VariablesNodeProps) => {
  const { manifest, onOpenSidebar, isLoading, relayMode } = data
  const currentRunningGroupId = useWorkflowStore(state => state.currentRunningGroupId)
  const selectedGroupIds = useWorkflowStore(state => state.selectedGroupIds) // Selected group IDs from checkboxes
  
  const variableCount = manifest?.variables?.length || 0
  const groupCount = manifest?.groups?.length || 0
  const totalGroups = groupCount || (variableCount > 0 ? 1 : 0)
  const enabledGroups = getEnabledGroups(manifest)
  const enabledCount = enabledGroups.length
  const isMultiGroup = hasMultipleGroups(manifest)

  // Count how many groups are selected via checkboxes
  const selectedCount = selectedGroupIds.length > 0
    ? selectedGroupIds.filter(id => manifest?.groups?.some(g => g.name === id && g.enabled)).length
    : 0

  // Handle node click to open sidebar
  const handleClick = useCallback((e: MouseEvent) => {
    e.stopPropagation()
    if (onOpenSidebar) {
      onOpenSidebar()
    }
  }, [onOpenSidebar])

  if (relayMode) {
    const configuration = (manifest?.variables ?? []).filter(variable => variable.name !== 'INPUT')
    return (
      <div className={`min-w-[220px] max-w-[300px] rounded-lg border-2 bg-purple-50 dark:bg-purple-900/20 border-purple-300 dark:border-purple-600 shadow-sm cursor-pointer ${selected ? 'ring-2 ring-purple-500/50' : ''}`} onClick={handleClick}>
        <Handle type="target" position={Position.Left} className="!bg-purple-400" />
        <div className="flex items-center gap-2 px-3 py-2 border-b border-purple-200 dark:border-purple-700">
          <Variable className="h-4 w-4 text-purple-600 dark:text-purple-400" />
          <span className="text-sm font-medium">Inputs & configuration</span>
          {isLoading && <span className="text-xs text-muted-foreground">Loading…</span>}
        </div>
        <div className="space-y-1 px-3 py-2 text-xs">
          {(manifest?.groups?.length ?? 0) > 1
            ? <p className="text-destructive">Legacy groups need configuration migration</p>
            : <p className="text-muted-foreground">One input object per run</p>}
          {configuration.slice(0, 3).map(variable => <p key={variable.name} className="truncate font-mono">{variable.name}</p>)}
          {configuration.length > 3 && <p className="text-muted-foreground">+{configuration.length - 3} configuration values</p>}
          <p className="text-muted-foreground">Click to edit configuration and sample input</p>
        </div>
        <Handle type="source" position={Position.Right} className="!bg-purple-400" />
      </div>
    )
  }

  // No variables and no groups yet
  if (!manifest || (variableCount === 0 && groupCount === 0)) {
    return (
      <div 
        className={`
          min-w-[200px] max-w-[280px] rounded-lg border-2 shadow-sm cursor-pointer
          bg-gray-50 dark:bg-gray-800/50 border-gray-300 dark:border-gray-600
          hover:border-purple-400 dark:hover:border-purple-500 transition-colors
          ${selected ? 'ring-2 ring-purple-500/50' : ''}
        `}
        onClick={handleClick}
      >
        {/* Input handle */}
        <Handle
          type="target"
          position={Position.Left}
          className="!w-3 !h-3 !bg-gray-400 dark:!bg-gray-500 !border-2 !border-white dark:!border-gray-800"
        />
        
        {/* Header */}
        <div className="flex items-center gap-2 px-3 py-2 bg-gray-100 dark:bg-gray-700/50 rounded-t-lg border-b border-gray-200 dark:border-gray-600">
          <Variable className="w-4 h-4 text-gray-500 dark:text-gray-400" />
          <span className="text-sm font-medium text-gray-600 dark:text-gray-300">
            Variables
          </span>
          {isLoading && (
            <span className="ml-auto text-xs text-gray-400">Loading...</span>
          )}
        </div>
        
        {/* Body */}
        <div className="px-3 py-2">
          <p className="text-xs text-gray-500 dark:text-gray-400 italic">
            No variables extracted yet
          </p>
          <p className="text-xs text-gray-400 dark:text-gray-500 mt-1">
            Click to add variables
          </p>
        </div>
        
        {/* Output handle */}
        <Handle
          type="source"
          position={Position.Right}
          className="!w-3 !h-3 !bg-gray-400 dark:!bg-gray-500 !border-2 !border-white dark:!border-gray-800"
        />
      </div>
    )
  }
  
  return (
    <div 
      className={`
        min-w-[220px] max-w-[300px] rounded-lg border-2 shadow-sm cursor-pointer
        bg-purple-50 dark:bg-purple-900/20 border-purple-300 dark:border-purple-600
        hover:border-purple-400 dark:hover:border-purple-500 transition-colors
        ${selected ? 'ring-2 ring-purple-500/50' : ''}
      `}
      onClick={handleClick}
    >
      {/* Input handle */}
      <Handle
        type="target"
        position={Position.Left}
        className="!w-3 !h-3 !bg-purple-400 dark:!bg-purple-500 !border-2 !border-white dark:!border-gray-800"
      />
      
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2 bg-purple-100 dark:bg-purple-800/30 rounded-t-lg border-b border-purple-200 dark:border-purple-700">
        <Variable className="w-4 h-4 text-purple-600 dark:text-purple-400" />
        <span className="text-sm font-medium text-purple-700 dark:text-purple-300">
          {variableCount > 0 ? `Variables (${variableCount})` : 'Groups'}
        </span>
        {isMultiGroup && (
          <span className="ml-auto flex items-center gap-1 text-xs text-purple-600 dark:text-purple-400">
            <Layers className="w-3 h-3" />
            {selectedCount > 0 ? `${selectedCount}/${enabledCount}` : enabledCount}/{totalGroups} Groups
            {selectedCount > 0 && (
              <Check className="w-3 h-3 text-green-600 dark:text-green-400" />
            )}
          </span>
        )}
      </div>
      
      {/* All groups with their values */}
      <div className="px-3 py-2 space-y-3">
        {(() => {
          // Get all groups (including virtual group for single-group mode)
          const allGroups = isMultiGroup && manifest.groups 
            ? manifest.groups 
            : enabledGroups
          return allGroups.map((group) => {
            const isRunning = currentRunningGroupId === group.name
            const isChecked = selectedGroupIds.includes(group.name) // Selected via checkbox

            return (
            <div
              key={group.name}
              className={`space-y-1.5 ${
                isRunning
                  ? 'bg-blue-50 dark:bg-blue-900/30 rounded p-1.5 border border-blue-200 dark:border-blue-700'
                  : isChecked
                  ? 'bg-purple-50 dark:bg-purple-900/30 rounded p-1.5 border-2 border-purple-300 dark:border-purple-600'
                  : ''
              }`}
            >
              {/* Group header */}
              <div className="flex items-center gap-1.5 text-xs">
                {isRunning ? (
                  <Play className="w-3 h-3 text-blue-600 dark:text-blue-400 animate-pulse" />
                ) : isChecked ? (
                  // When checked via checkbox, show checkmark
                  <Check className="w-3 h-3 text-green-600 dark:text-green-400" />
                ) : group.enabled ? (
                  // When not checked but enabled, show enabled indicator
                  <CheckCircle className="w-3 h-3 text-green-600 dark:text-green-400" />
                ) : (
                  // When disabled, show empty circle
                  <Circle className="w-3 h-3 text-gray-400 dark:text-gray-500" />
                )}
                <span className={`font-semibold ${
                  isRunning
                    ? 'text-blue-700 dark:text-blue-300'
                    : isChecked
                    ? 'text-purple-700 dark:text-purple-300'
                    : 'text-purple-600 dark:text-purple-400'
                }`}>
                  {group.name}
                </span>
                {isRunning && (
                  <span className="ml-auto text-[10px] text-blue-600 dark:text-blue-400 font-medium">
                    Running...
                  </span>
                )}
                {!isRunning && isChecked && (
                  <span className="ml-auto text-[10px] text-green-600 dark:text-green-400 font-medium">
                    Selected
                  </span>
                )}
              </div>
            
              {/* Variables for this group */}
            <div className="ml-4 space-y-1">
              {manifest.variables && manifest.variables.slice(0, 3).map((variable, idx) => {
                const value = getGroupValues(group)[variable.name] || ''
                return (
                  <div key={idx} className="flex flex-col gap-0.5 text-xs">
                    <div className="flex items-center gap-1">
                      <span className="text-gray-500 dark:text-gray-400">•</span>
                      <span className="text-gray-700 dark:text-gray-300 font-mono">
                        {variable.name}
                      </span>
                    </div>
                    <div className="ml-3 text-gray-600 dark:text-gray-400 font-mono text-[10px] truncate">
                      {value ? value : <span className="italic text-gray-400 dark:text-gray-500">(empty)</span>}
                    </div>
                  </div>
                )
              })}
              {manifest.variables && manifest.variables.length > 3 && (
                <div className="text-[10px] text-gray-400 dark:text-gray-500 italic ml-3">
                  +{manifest.variables.length - 3} more...
                </div>
              )}
            </div>
          </div>
            )
          })
        })()}
      </div>
      
      {/* Groups section (only if multiple groups) */}
      {isMultiGroup && manifest.groups && (
        <div className="px-3 py-2 border-t border-purple-200 dark:border-purple-700">
          <div className="flex flex-wrap gap-1.5">
            {manifest.groups.slice(0, 6).map((group) => {
              const isRunning = currentRunningGroupId === group.name
              const isChecked = selectedGroupIds.includes(group.name) // Selected via checkbox

              return (
              <div
                key={group.name}
                className={`
                  flex items-center gap-1 px-1.5 py-0.5 rounded text-xs
                  ${isRunning
                    ? 'bg-blue-200 dark:bg-blue-700/50 text-blue-700 dark:text-blue-300 border border-blue-300 dark:border-blue-600'
                    : isChecked
                    ? 'bg-purple-300 dark:bg-purple-700/70 text-purple-800 dark:text-purple-200 border-2 border-purple-400 dark:border-purple-500'
                    : group.enabled
                    ? 'bg-purple-200 dark:bg-purple-700/50 text-purple-700 dark:text-purple-300'
                    : 'bg-gray-200 dark:bg-gray-700 text-gray-500 dark:text-gray-400 opacity-60'}
                `}
                title={isRunning ? 'Currently Running' : isChecked ? 'Selected for Execution' : group.enabled ? 'Enabled' : 'Disabled'}
              >
                {isRunning ? (
                  <Play className="w-3 h-3 animate-pulse" />
                ) : isChecked ? (
                  // When checked, show checkmark
                  <Check className="w-3 h-3" />
                ) : group.enabled ? (
                  // When not checked but enabled, show enabled indicator
                  <CheckCircle className="w-3 h-3" />
                ) : (
                  // When disabled, show empty circle
                  <Circle className="w-3 h-3" />
                )}
                <span>
                  {group.name}
                </span>
              </div>
            )
            })}
            {manifest.groups.length > 6 && (
              <span className="text-xs text-gray-500 dark:text-gray-400">
                +{manifest.groups.length - 6}
              </span>
            )}
          </div>
        </div>
      )}
      
      {/* Output handle */}
      <Handle
        type="source"
        position={Position.Right}
        className="!w-3 !h-3 !bg-purple-400 dark:!bg-purple-500 !border-2 !border-white dark:!border-gray-800"
      />
    </div>
  )
})

VariablesNode.displayName = 'VariablesNode'

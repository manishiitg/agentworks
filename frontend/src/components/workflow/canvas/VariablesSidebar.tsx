import React, { useState, useEffect, useCallback } from 'react'
import { X, Plus, Trash2, Save, Check, Circle, Loader2, AlertCircle } from 'lucide-react'
import { agentApi } from '../../../services/api'
import type { VariablesManifest, VariableGroup } from '../../../services/api-types'

interface VariablesSidebarProps {
  workspacePath: string | null
  relayMode?: boolean
  onClose: () => void
  onUpdate?: (manifest: VariablesManifest) => void
  showChatArea?: boolean  // When true, use lower z-index so ChatArea appears on top
}

const getGroupValues = (group: VariableGroup): Record<string, string> => {
  return group.values && typeof group.values === 'object' ? group.values : {}
}

export const VariablesSidebar: React.FC<VariablesSidebarProps> = ({
  workspacePath,
  onClose,
  onUpdate,
  showChatArea = false,
  relayMode = false
}) => {
  const [manifest, setManifest] = useState<VariablesManifest | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [hasChanges, setHasChanges] = useState(false)
  const [migrationBlocked, setMigrationBlocked] = useState(false)

  // Load variable groups
  useEffect(() => {
    if (!workspacePath) {
      setIsLoading(false)
      return
    }

    const loadVariables = async () => {
      setIsLoading(true)
      setError(null)
      setMigrationBlocked(false)
      try {
        const response = await agentApi.getVariableGroups(workspacePath)
        if (!response.success) throw new Error(response.error || 'Failed to load variables')
        if (response.success && response.manifest) {
          if (relayMode && (response.manifest.groups?.length ?? 0) > 1) {
            setManifest(response.manifest)
            setMigrationBlocked(true)
            setError('This legacy Relay has multiple variable groups. Ask the Builder to choose the configuration values to keep and remove the groups before editing here.')
          } else if (relayMode) {
            const normalized = { ...response.manifest, variables: (response.manifest.variables ?? []).map(variable => ({ ...variable })) }
            for (const [name, value] of Object.entries(response.manifest.groups?.[0]?.values ?? {})) {
              const existing = normalized.variables.find(variable => variable.name === name)
              if (existing) existing.value = value
              else normalized.variables.push({ name, value, description: '' })
            }
            delete normalized.groups
            setManifest(normalized)
          } else {
            setManifest(response.manifest)
          }
        } else {
          setManifest(null)
        }
      } catch (err) {
        console.error('[VariablesSidebar] Failed to load variables:', err)
        const responseMessage = (err as { response?: { data?: unknown } })?.response?.data
        setError(typeof responseMessage === 'string' ? responseMessage : err instanceof Error ? err.message : 'Failed to load variables')
        // Never overwrite legacy configuration after an ambiguous or failed read.
        if (relayMode) setMigrationBlocked(true)
      } finally {
        setIsLoading(false)
      }
    }

    loadVariables()
  }, [workspacePath, relayMode])

  // Get groups (handling single-group backward compatibility)
  const getGroups = useCallback((): VariableGroup[] => {
    if (!manifest) return []
    // If groups exist, use them directly
    if (manifest.groups && manifest.groups.length > 0) {
      return manifest.groups
    }
    // Single group mode - create virtual group from variables
    // Only create virtual group if we have variables but no groups
    if (manifest.variables && manifest.variables.length > 0) {
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
    return []
  }, [manifest])

  const groups = getGroups()

  // Add new group
  const handleAddGroup = useCallback(() => {
    if (!manifest) {
      // No manifest yet — create one with empty variables and a first group
      const newManifest: VariablesManifest = {
        objective: '',
        extraction_date: new Date().toISOString(),
        variables: [],
        groups: [{ name: 'group-1', values: {}, enabled: true }]
      }
      setManifest(newManifest)
      setHasChanges(true)
      return
    }

    // If this is the first group being added, migrate existing values to group-1
    const isFirstGroup = !manifest.groups || manifest.groups.length === 0
    const variables = manifest.variables ?? []

    let newGroupId: string
    let updatedGroups: VariableGroup[]

    if (isFirstGroup) {
      // Migrate existing values to group-1, new group will be group-2
      const existingValues: Record<string, string> = {}
      variables.forEach(v => {
        existingValues[v.name] = v.value || ''
      })

      const newValues: Record<string, string> = {}
      variables.forEach(v => {
        newValues[v.name] = '' // Empty values for new group
      })

      newGroupId = 'group-2'
      updatedGroups = [
        { name: 'group-1', values: existingValues, enabled: true },
        { name: newGroupId, values: newValues, enabled: true }
      ]
    } else {
      // Add to existing groups
      const existingGroups = manifest.groups || []
      newGroupId = `group-${existingGroups.length + 1}`
      const newValues: Record<string, string> = {}
      variables.forEach(v => {
        newValues[v.name] = '' // Empty values for new group
      })

      updatedGroups = [
        ...existingGroups,
        { name: newGroupId, values: newValues, enabled: true }
      ]
    }

    const updatedManifest: VariablesManifest = {
      ...manifest,
      groups: updatedGroups
    }

    setManifest(updatedManifest)
    setHasChanges(true)
  }, [manifest])

  // Toggle group enabled/disabled
  const handleToggleGroup = useCallback((groupName: string) => {
    if (!manifest?.groups) return

    const updatedGroups = manifest.groups.map(g =>
      g.name === groupName ? { ...g, enabled: !g.enabled } : g
    )
    
    const updatedManifest = { ...manifest, groups: updatedGroups }
    setManifest(updatedManifest)
    setHasChanges(true)
  }, [manifest])

  // Delete group
  const handleDeleteGroup = useCallback((groupName: string) => {
    if (!manifest?.groups || manifest.groups.length <= 1) return

    const updatedGroups = manifest.groups.filter(g => g.name !== groupName)
    const updatedManifest = { ...manifest, groups: updatedGroups }
    setManifest(updatedManifest)
    setHasChanges(true)
  }, [manifest])

  // Update variable value in single-group mode
  const handleUpdateValue = useCallback((variableName: string, newValue: string) => {
    if (!manifest) return
    
    // Single-group mode - update Variables[].value directly
    const updatedVariables = (manifest.variables ?? []).map(v =>
      v.name === variableName ? { ...v, value: newValue } : v
    )
    const updatedManifest = { ...manifest, variables: updatedVariables }
    setManifest(updatedManifest)
    setHasChanges(true)
  }, [manifest])

  const handleSampleInput = useCallback((value: string) => {
    const current = manifest ?? { objective: '', extraction_date: new Date().toISOString(), variables: [] }
    const variables = current.variables ?? []
    setManifest({
      ...current,
      variables: variables.some(variable => variable.name === 'INPUT')
        ? variables.map(variable => variable.name === 'INPUT' ? { ...variable, value } : variable)
        : [...variables, { name: 'INPUT', description: 'Sample input for Builder test runs', value }],
    })
    setHasChanges(true)
  }, [manifest])

  // Save changes
  const handleSave = useCallback(async () => {
    if (!manifest || !workspacePath || !hasChanges || migrationBlocked) return
    
    setIsSaving(true)
    setError(null)
    try {
      const response = await agentApi.updateVariableGroups(workspacePath, manifest)
      if (!response.success) {
        throw new Error(response.message || 'Failed to save changes')
      }
      setHasChanges(false)
      if (onUpdate) {
        onUpdate(manifest)
      }
    } catch (err) {
      console.error('[VariablesSidebar] Failed to save:', err)
      let errorMessage = 'Failed to save changes'
      if (err instanceof Error) {
        errorMessage = err.message
      } else if (typeof err === 'object' && err !== null && 'response' in err) {
        const axiosError = err as { response?: { data?: { message?: string } } }
        errorMessage = axiosError.response?.data?.message || errorMessage
      }
      setError(errorMessage)
    } finally {
      setIsSaving(false)
    }
  }, [manifest, workspacePath, hasChanges, onUpdate, migrationBlocked])


  // Count enabled groups
  const enabledCount = groups.filter(g => g.enabled).length
  const isMultiGroup = groups.length > 1

  return (
    <div className={`absolute right-0 top-0 bottom-0 w-[450px] bg-background border-l border-border flex flex-col shadow-lg ${showChatArea ? 'z-30' : 'z-50'}`}>
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-border bg-background">
        <div className="flex items-center gap-2">
          <h2 className="text-base font-semibold text-foreground">{relayMode ? 'Inputs & configuration' : 'Variables'}</h2>
          {!relayMode && isMultiGroup && (
            <span className="text-xs text-muted-foreground bg-muted px-2 py-0.5 rounded">
              {enabledCount}/{groups.length} groups enabled
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          {hasChanges && (
            <button
              onClick={handleSave}
              disabled={isSaving}
              className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded
                       bg-purple-600 dark:bg-purple-500 text-white hover:bg-purple-700 dark:hover:bg-purple-600 
                       disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {isSaving ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
              ) : (
                <Save className="w-3.5 h-3.5" />
              )}
              Save
            </button>
          )}
          <button
            onClick={onClose}
            className="p-1.5 rounded hover:bg-muted text-muted-foreground hover:text-foreground"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Error display */}
      {error && (
        <div className="mx-4 mt-3 p-3 rounded-md bg-destructive/10 border border-destructive/20 text-destructive dark:text-destructive text-sm flex items-center gap-2">
          <AlertCircle className="w-4 h-4 flex-shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {/* Loading state */}
      {isLoading ? (
        <div className="flex-1 flex items-center justify-center">
          <Loader2 className="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
      ) : relayMode ? (
        <div className="flex-1 overflow-y-auto space-y-6 p-4">
          <section className="space-y-2">
            <h3 className="text-sm font-semibold">Sample input</h3>
            <p className="text-xs text-muted-foreground">JSON INPUT for testing in Builder chat. API calls supply their own input for each run.</p>
            <textarea
              aria-label="Sample INPUT JSON"
              disabled={migrationBlocked}
              value={manifest?.variables?.find(variable => variable.name === 'INPUT')?.value ?? ''}
              onChange={event => handleSampleInput(event.target.value)}
              placeholder={'{ "example": "value" }'}
              rows={6}
              className="w-full rounded border border-border bg-muted/30 p-3 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-primary/50"
            />
          </section>
          <section className="space-y-3">
            <h3 className="text-sm font-semibold">Configuration</h3>
            <p className="text-xs text-muted-foreground">Reusable values for this Relay. Ask the Builder to add or change configuration variables. Manage credentials in Secrets.</p>
            {(manifest?.variables ?? []).filter(variable => variable.name !== 'INPUT').map(variable => (
              <label key={variable.name} className="block space-y-1 text-xs">
                <span className="font-mono font-medium">{variable.name}</span>
                {variable.description && <span className="block text-muted-foreground">{variable.description}</span>}
                <input
                  aria-label={variable.name}
                  disabled={migrationBlocked}
                  value={variable.value ?? ''}
                  onChange={event => handleUpdateValue(variable.name, event.target.value)}
                  className="w-full rounded border border-border bg-muted/30 px-3 py-2 font-mono text-sm focus:outline-none focus:ring-2 focus:ring-primary/50"
                />
              </label>
            ))}
          </section>
        </div>
      ) : (
        <div className="flex-1 overflow-y-auto flex flex-col">
          {/* Add group button */}
          <div className="px-4 py-2 border-b border-border flex-shrink-0">
            <button
              onClick={handleAddGroup}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium
                       bg-muted text-muted-foreground hover:bg-muted/80 transition-colors w-full justify-center"
            >
              <Plus className="w-3.5 h-3.5" />
              {isMultiGroup ? 'Add Group' : 'Add Variable Group'}
            </button>
            {!isMultiGroup && (
              <p className="text-xs text-muted-foreground mt-1.5 text-center">
                {(!manifest || (manifest.variables ?? []).length === 0)
                  ? 'No variables extracted. You can still add groups as named execution runs.'
                  : 'Add multiple groups to run automation with different values'}
              </p>
            )}
          </div>

          {/* All groups list */}
          <div className="p-4 space-y-4">
            {groups.map((group, index) => (
              <div
                key={`${group.name}-${index}`}
                className={`border rounded-lg p-3 space-y-3 ${
                  !group.enabled ? 'opacity-60 border-border/50' : 'border-border'
                }`}
              >
                {/* Group header */}
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 flex-1 min-w-0">
                    <button
                      onClick={() => handleToggleGroup(group.name)}
                      className="flex items-center gap-1.5 hover:scale-110 transition-transform flex-shrink-0"
                      title={group.enabled ? 'Disable group' : 'Enable group'}
                    >
                      {group.enabled ? (
                        <Check className="w-4 h-4 text-green-600 dark:text-green-400" />
                      ) : (
                        <Circle className="w-4 h-4 text-gray-400 dark:text-gray-500" />
                      )}
                    </button>
                    <div className="flex items-center gap-2 flex-1 min-w-0">
                      <input
                        type="text"
                        value={group.name}
                        onChange={(e) => {
                          // Update name for this specific group
                          // Handle both cases: when groups exist in manifest, and when we're in single-group mode
                          if (!manifest) return
                          if (manifest.groups && manifest.groups.length > 0) {
                            // Multi-group mode: update existing groups
                            const updatedGroups = manifest.groups.map(g => {
                              if (g.name === group.name) {
                                return { ...g, name: e.target.value }
                              }
                              return g
                            })
                            const updatedManifest = { ...manifest, groups: updatedGroups }
                            setManifest(updatedManifest)
                            setHasChanges(true)
                          } else {
                            // Single-group mode: create groups array from virtual group
                            // Migrate existing values to a proper group structure
                            const values: Record<string, string> = {}
                            ;(manifest.variables ?? []).forEach(v => {
                              values[v.name] = v.value || ''
                            })
                            const updatedGroups = [{
                              ...group,
                              name: e.target.value,
                              values
                            }]
                            const updatedManifest = { ...manifest, groups: updatedGroups }
                            setManifest(updatedManifest)
                            setHasChanges(true)
                          }
                        }}
                        placeholder="Group name"
                        className="flex-1 px-2 py-1 rounded border border-border bg-background text-sm font-semibold text-foreground
                                 focus:outline-none focus:ring-2 focus:ring-primary/50 focus:border-primary
                                 placeholder:text-muted-foreground/50 min-w-0"
                        title="Edit group name"
                      />
                    </div>
                    {!group.enabled && (
                      <span className="text-xs text-muted-foreground flex-shrink-0">(Disabled)</span>
                    )}
                  </div>
                  {groups.length > 1 && (
                    <button
                      onClick={() => handleDeleteGroup(group.name)}
                      className="p-1 hover:text-red-500 transition-colors flex-shrink-0 ml-2"
                      title="Delete group"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  )}
                </div>

                {/* Variables for this group */}
                <div className="space-y-2">
                  {(manifest?.variables ?? []).map((variable) => (
                    <div key={variable.name} className="space-y-1">
                      <label className="flex items-center gap-2 text-xs font-medium text-foreground">
                        <span className="font-mono text-purple-600 dark:text-purple-400">
                          {variable.name}
                        </span>
                        {variable.description && (
                          <span className="text-xs text-muted-foreground font-normal">
                            ({variable.description})
                          </span>
                        )}
                      </label>
                      <input
                        type="text"
                        value={getGroupValues(group)[variable.name] || ''}
                        onChange={(e) => {
                          // Update value for this specific group
                          if (!manifest) return
                          if (manifest.groups && manifest.groups.length > 0) {
                            const updatedGroups = manifest.groups.map(g => {
                              if (g.name === group.name) {
                                return { ...g, values: { ...getGroupValues(g), [variable.name]: e.target.value } }
                              }
                              return g
                            })
                            const updatedManifest = { ...manifest, groups: updatedGroups }
                            setManifest(updatedManifest)
                            setHasChanges(true)
                          } else {
                            // Single group mode
                            handleUpdateValue(variable.name, e.target.value)
                          }
                        }}
                        disabled={!group.enabled}
                        className="w-full px-3 py-2 rounded border border-border bg-muted/30 text-foreground text-sm
                                 focus:outline-none focus:ring-2 focus:ring-primary/50 focus:border-primary focus:bg-background
                                 font-mono placeholder:text-muted-foreground/50 disabled:opacity-50 disabled:cursor-not-allowed"
                        placeholder={`Enter value for ${variable.name}`}
                      />
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

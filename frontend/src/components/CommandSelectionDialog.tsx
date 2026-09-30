import React, { useState, useEffect, useRef, useMemo, useSyncExternalStore, useId } from 'react'
import { Terminal, Pencil, Trash2, Plus } from 'lucide-react'
import type { ModeCategory } from '../stores/useModeStore'
import { findCommand, findProductOrUserCommand, getCommands, getProductAndUserCommands, type CommandDefinition, type WorkshopMode } from '../commands'
import { loadAndRegisterUserCommands } from '../commands'

import { subscribeCommands, getCommandRevision } from '../commands/registry'
import { isPlainPickerKey } from '../utils/composerReferences'
import { useComposerPicker, isComposerPickerEvent, type ComposerPickerProps } from '../hooks/useComposerPicker'

interface CommandSelectionDialogProps extends ComposerPickerProps {
  standalone?: boolean
  onSearchQueryChange?: (query: string) => void
  triggerRef?: React.RefObject<HTMLElement | null>
  isOpen: boolean
  onClose: (reason?: 'outside' | 'resize') => void
  onSelectCommand: (command: string) => void
  searchQuery: string
  position: { bottom: number; left: number }
  modeCategory?: ModeCategory
  workshopMode?: WorkshopMode
  canWriteWorkflow?: boolean
  agentProfileId?: string
  workspacePath?: string
  onCreateCommand?: () => void
  onEditCommand?: (command: CommandDefinition) => void
  onDeleteCommand?: (command: CommandDefinition) => void
}

export const CommandSelectionDialog: React.FC<CommandSelectionDialogProps> = ({
  isOpen,
  onClose,
  onSelectCommand,
  searchQuery,
  position,
  modeCategory,
  workshopMode,
  canWriteWorkflow = true,
  agentProfileId,
  workspacePath,
  onCreateCommand,
  onEditCommand,
  onDeleteCommand, inputRef, listId, onActiveOptionChange,
  standalone = false, onSearchQueryChange, triggerRef
}) => {
  const [selectedIndex, setSelectedIndex] = useState(0)
  const revision = useSyncExternalStore(subscribeCommands, getCommandRevision)
  const generatedId = useId()
  const optionListId = listId ?? generatedId
  const dialogRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const pickerInputRef = standalone ? searchInputRef : inputRef

  useEffect(() => {
    if (isOpen && standalone) searchInputRef.current?.focus()
  }, [isOpen, standalone])

  useEffect(() => {
    if (isOpen) {
      loadAndRegisterUserCommands(workspacePath).catch(() => {})
    }
  }, [isOpen, workspacePath])

  // Filter commands based on search query and current mode
  const filteredCommands = useMemo(() => {
    const allCommands = agentProfileId
      ? getProductAndUserCommands(modeCategory, workshopMode, canWriteWorkflow)
      : getCommands(modeCategory, workshopMode, canWriteWorkflow)

    if (!searchQuery.trim()) {
      return allCommands
    }

    const query = searchQuery.toLowerCase().trim()
    const filtered = allCommands.filter(cmd =>
      cmd.command.toLowerCase().includes(query) ||
      cmd.description.toLowerCase().includes(query) ||
      cmd.searchTerms?.some(term => term.toLowerCase().includes(query))
    )

    // Sort by relevance: exact match first, then partial match
    filtered.sort((a, b) => {
      const aExact = a.command.toLowerCase() === query
      const bExact = b.command.toLowerCase() === query
      if (aExact && !bExact) return -1
      if (!aExact && bExact) return 1

      const aStarts = a.command.toLowerCase().startsWith(query)
      const bStarts = b.command.toLowerCase().startsWith(query)
      if (aStarts && !bStarts) return -1
      if (!aStarts && bStarts) return 1

      return a.command.localeCompare(b.command)
    })

    return filtered
  }, [agentProfileId, searchQuery, modeCategory, workshopMode, canWriteWorkflow, revision])

  useEffect(() => { setSelectedIndex(0) }, [searchQuery, isOpen, filteredCommands])
  useEffect(() => {
    if (isOpen) onActiveOptionChange?.(filteredCommands[selectedIndex] ? `${optionListId}-${selectedIndex}` : undefined)
  }, [isOpen, filteredCommands, selectedIndex, optionListId, onActiveOptionChange])
  useComposerPicker(isOpen, dialogRef, pickerInputRef, onClose, triggerRef)

  // Keyboard shortcuts
  useEffect(() => {
    if (!isOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isComposerPickerEvent(event, pickerInputRef) || !isPlainPickerKey(event)) return
      if (event.key === 'Tab') { onClose(); return }
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
      } else if (event.key === 'Enter') {
        event.preventDefault()
        // Exact retained shortcuts remain directly executable even though the
        // search results point to the consolidated command's focus picker.
        const shortcut = agentProfileId
          ? findProductOrUserCommand(searchQuery.trim(), modeCategory, workshopMode, canWriteWorkflow)
          : findCommand(searchQuery.trim(), modeCategory, workshopMode, canWriteWorkflow)
        if (selectedIndex === 0 && shortcut?.menuHidden) {
          onSelectCommand(shortcut.command)
          return
        }
        if (filteredCommands.length > 0 && selectedIndex >= 0 && selectedIndex < filteredCommands.length) {
          onSelectCommand(filteredCommands[selectedIndex].command)
        } else { onClose() }
      } else if (event.key === 'ArrowDown') {
        event.preventDefault()
        setSelectedIndex(prev => Math.max(0, Math.min(prev + 1, filteredCommands.length - 1)))
      } else if (event.key === 'ArrowUp') {
        event.preventDefault()
        setSelectedIndex(prev => Math.max(prev - 1, 0))
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [agentProfileId, isOpen, onClose, onSelectCommand, filteredCommands, selectedIndex, searchQuery, modeCategory, workshopMode, canWriteWorkflow, pickerInputRef])

  // Scroll selected item into view
  useEffect(() => {
    if (listRef.current && selectedIndex >= 0) {
      const selectedElement = listRef.current.children[selectedIndex] as HTMLElement
      if (selectedElement) {
        selectedElement.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
      }
    }
  }, [selectedIndex])

  if (!isOpen) return null

  return (
    <div
      ref={dialogRef}
      className="fixed z-50 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg flex flex-col overflow-hidden"
      style={{
        bottom: Math.max(8, Math.min(position.bottom, window.innerHeight - 80)),
        left: Math.max(8, Math.min(position.left, window.innerWidth - Math.min(420, window.innerWidth - 16) - 8)),
        width: 'min(420px, calc(100vw - 16px))',
        maxHeight: `calc(100vh - ${Math.max(8, Math.min(position.bottom, window.innerHeight - 80)) + 8}px)`
      }}
    >
      {/* Header */}
      <div className="px-3 py-2 border-b border-border bg-secondary">
        <div className="flex items-center gap-2">
          <Terminal className="w-4 h-4 text-muted-foreground" />
          <span className="text-sm font-medium">Commands</span>
        </div>
      </div>

      {standalone && (
        <input ref={searchInputRef} type="text" value={searchQuery} onChange={event => onSearchQueryChange?.(event.target.value)}
          aria-label="Search commands" role="combobox" aria-autocomplete="list" aria-expanded={isOpen}
          aria-controls={optionListId} aria-activedescendant={filteredCommands[selectedIndex] ? `${optionListId}-${selectedIndex}` : undefined}
          placeholder="Search commands…" className="border-b border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus:ring-1 focus:ring-inset focus:ring-ring" />
      )}

      {/* Command List */}
      <div
        ref={listRef}
        id={optionListId}
        role="listbox"
        aria-label="Commands"
        className="overflow-y-auto max-h-96"
      >
        {filteredCommands.length === 0 ? (
          <div className="px-3 py-4 text-center text-muted-foreground text-sm">
            {searchQuery ? 'No commands found' : 'No commands available'}
          </div>
        ) : (
          filteredCommands.map((cmd, index) => (
            <div
              key={cmd.command}
              id={`${optionListId}-${index}`}
              role="option"
              aria-selected={index === selectedIndex}
              onMouseDown={event => event.preventDefault()}
              className={`group px-3 py-2 cursor-pointer flex items-center gap-2 text-sm transition-colors ${
                index === selectedIndex
                  ? 'bg-primary/10 text-primary border-l-2 border-primary'
                  : 'hover:bg-secondary'
              }`}
              onClick={() => onSelectCommand(cmd.command)}
            >
              <div className="text-muted-foreground">
                {cmd.icon}
              </div>
              <div className="flex-1 min-w-0">
                <div className="font-medium">/{cmd.command}</div>
                <div className="text-xs text-muted-foreground truncate">
                  {cmd.description}
                </div>
              </div>
              {cmd.source === 'user' && (
                <div className="hidden group-hover:flex items-center gap-1">
                  {onEditCommand && (
                    <button
                      type="button"
                      className="p-1 hover:bg-gray-200 dark:hover:bg-gray-600 rounded"
                      onClick={(e) => { e.stopPropagation(); onEditCommand(cmd) }}
                      title="Edit command"
                    >
                      <Pencil className="w-3 h-3" />
                    </button>
                  )}
                  {onDeleteCommand && (
                    <button
                      type="button"
                      className="p-1 hover:bg-red-100 dark:hover:bg-red-900/30 rounded text-red-500"
                      onClick={(e) => { e.stopPropagation(); onDeleteCommand(cmd) }}
                      title="Delete command"
                    >
                      <Trash2 className="w-3 h-3" />
                    </button>
                  )}
                </div>
              )}
            </div>
          ))
        )}
      </div>

      {onCreateCommand && (
        <button
          type="button"
          onMouseDown={event => event.preventDefault()}
          onClick={onCreateCommand}
          className="flex items-center gap-2 border-t border-border px-3 py-2 text-sm text-muted-foreground hover:bg-secondary hover:text-foreground"
        >
          <Plus className="h-4 w-4" />
          <span>Ask the agent to create a command</span>
        </button>
      )}

      {/* Footer */}
      <div className="px-3 py-2 border-t border-border bg-secondary text-xs text-muted-foreground">
        <div className="flex items-center justify-between">
          <span>↑↓ to navigate</span>
          <span>Enter to select</span>
        </div>
      </div>
    </div>
  )
}

export default CommandSelectionDialog

import { useEffect, useCallback, useRef, useMemo, useState, type ReactNode } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { Upload, FolderPlus, ChevronsDownUp, CheckSquare, X, Trash2, Loader2, Eye, EyeOff, Search, RefreshCw } from 'lucide-react'
import { agentApi, workspaceApi } from '../services/api'
import type { PlannerFile } from '../services/api-types'
import PlannerFileList from './workspace/PlannerFileList'
import { SessionInstructions } from './workspace/SessionInstructions'
import { openWorkspaceFile } from '../utils/openWorkspaceFile'
import CreateFolderDialog from './workspace/CreateFolderDialog'
import MoveFileDialog from './workspace/MoveFileDialog'
import RenameFileDialog from './workspace/RenameFileDialog'
import ConfirmationDialog from './ui/ConfirmationDialog'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './ui/tooltip'
import { ExplorerHeader } from './workspace/ExplorerHeader'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useCapabilitiesStore } from '../stores/useCapabilitiesStore'
import { useModeStore } from '../stores/useModeStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { useChatStore } from '../stores/useChatStore'
import { activeWorkflowTabIdForPreset } from '../utils/workflowTabOwnership'
import { useAppStore } from '../stores/useAppStore'
import { useAuthStore } from '../stores/useAuthStore'
import { usePresetApplication } from '../stores/useGlobalPresetStore'
import { useActiveWorkflowPreset } from '../hooks/useActiveWorkflowPreset'
import {
  collectFolderPaths,
  EXPAND_FIRST_LEVEL_FOLDERS_BY_DEFAULT,
  getInitialExpandedWorkspaceFolders,
  hideManagedWorkspaceRootEntries,
  restoreExpandedFolders,
  getOriginalPath,
  resolveWorkspaceUploadPath,
  isPathWithinFolder,
  publicWorkspacePathForUser,
  adjustFilePathsRecursive
} from '../utils/workspacePathUtils'
import { useIterationExpansion } from './workspace/useIterationExpansion'
import { isProtectedWorkspaceEntry, workspaceSelectionItems } from '../utils/workspaceSelection'
import { scopeFilesToWorkspace } from '../utils/scopedWorkspaceTree'

interface WorkspaceProps {
  /** Restrict the reusable Files experience to one trusted workspace root. */
  scopedWorkspacePath?: string
  /** Provider/runtime directories to keep out of a creator-facing file tree. */
  hiddenRootFolders?: string[]
  /** Hide the per-row shortcut that adds a file or folder to chat context. */
  hideAddToChat?: boolean
  /** Hide actions for the scoped workspace root while retaining child actions. */
  hideRootActions?: boolean
  /** Opt in to opening direct child folders when the workspace is first shown. */
  expandFirstLevelFolders?: boolean
  /** Hide platform-owned root files/folders until the user reveals them. */
  hideManagedEntriesByDefault?: boolean
  title?: string
  headerAction?: ReactNode
}

type DownloadStatus = {
  path: string
  name: string
  loaded: number
  total?: number
}

function formatTransferSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  return `${value.toFixed(value >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`
}

export default function Workspace({
  scopedWorkspacePath,
  hiddenRootFolders = [],
  hideAddToChat = false,
  hideRootActions = false,
  expandFirstLevelFolders = EXPAND_FIRST_LEVEL_FOLDERS_BY_DEFAULT,
  hideManagedEntriesByDefault = false,
  title = 'Workspace',
  headerAction,
}: WorkspaceProps) {
  // Get mode-specific file context and handlers
  const selectedModeCategory = useModeStore(state => state.selectedModeCategory)
  const authUser = useAuthStore(state => state.user)
  const protectedRootPath = hideRootActions && scopedWorkspacePath ? publicWorkspacePathForUser(scopedWorkspacePath, authUser?.id) : undefined
  const isMultiUserMode = useAuthStore(state => state.isMultiUserMode)
  // The managed-files eye (product.json, .git, node_modules) is for admins.
  const canShowManagedFiles = !isMultiUserMode || authUser?.is_admin === true
  const currentUserFolder = `_users/${authUser?.id || 'default'}`
  const showWorkflowsOverview = useAppStore(state => state.showWorkflowsOverview)
  const showSchedulesOverview = useAppStore(state => state.showSchedulesOverview)
  const getActiveTab = useChatStore(state => state.getActiveTab)
  const setTabConfig = useChatStore(state => state.setTabConfig)
  const { getActivePreset } = usePresetApplication()

  // Get file context based on mode: multi-agent mode uses tab config, workflow mode uses preset
  const chatFileContext = useMemo(() => {
    if (selectedModeCategory === 'multi-agent') {
      const activeTab = getActiveTab()
      return activeTab?.config?.fileContext || []
    } else if (selectedModeCategory === 'workflow') {
      const activePreset = getActivePreset('workflow')
      if (activePreset?.selectedFolder) {
        return [{
          name: activePreset.selectedFolder.filepath.split('/').pop() || '',
          path: activePreset.selectedFolder.filepath,
          type: (activePreset.selectedFolder.type || 'folder') as 'file' | 'folder'
        }]
      }
    }
    return []
  }, [selectedModeCategory, getActiveTab, getActivePreset])

  // Add file to context handler - mode-specific
  const addFileToContext = useCallback((file: { name: string; path: string; type: 'file' | 'folder' }) => {
    if (selectedModeCategory === 'multi-agent') {
      const activeTab = getActiveTab()
      if (activeTab) {
        const currentContext = activeTab.config?.fileContext || []
        setTabConfig(activeTab.tabId, {
          fileContext: [...currentContext, file]
        })
      }
    }
    // Workflow mode doesn't support adding files to context (preset folder is fixed)
  }, [selectedModeCategory, getActiveTab, setTabConfig])

  // Get active workflow preset to filter workspace to selected folder
  const activeWorkflowPreset = useActiveWorkflowPreset()

  // Multi-select state
  const [isSelectionMode, setIsSelectionMode] = useState(false)
  const [selectedFiles, setSelectedFiles] = useState<Set<string>>(new Set())
  const [bulkDeleteDialog, setBulkDeleteDialog] = useState<{
    isOpen: boolean
    isLoading: boolean
    items: PlannerFile[]
  }>({
    isOpen: false,
    isLoading: false,
    items: []
  })

  // Server refresh search state (re-fetch file tree when local search finds nothing)
  const [serverSearchLoading, setServerSearchLoading] = useState(false)
  const [showHiddenFiles, setShowHiddenFiles] = useState(false)

  // Multi-file upload state
  const [pendingFiles, setPendingFiles] = useState<File[]>([])
  const [isDragOver, setIsDragOver] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<{ current: number; total: number } | null>(null)
  const [uploadResults, setUploadResults] = useState<{ name: string; success: boolean; error?: string }[] | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [downloadStatus, setDownloadStatus] = useState<DownloadStatus | null>(null)

  const {
    files,
    loading,
    error,
    setError,
    searchQuery,
    setSearchQuery,
    uploadDialog,
    setUploadDialog,
    openUploadDialog,
    closeUploadDialog,
    createFolderDialog,
    openCreateFolderDialog,
    closeCreateFolderDialog,
    deleteDialog,
    setDeleteDialog,
    openDeleteDialog,
    closeDeleteDialog,
    deleteAllFilesDialog,
    setDeleteAllFilesDialog,
    openDeleteAllFilesDialog,
    closeDeleteAllFilesDialog,
    showActionsDropdown,
    setShowActionsDropdown,
    expandedFolders,
    setExpandedFolders,
    expandFoldersToLevel,
    toggleFolder,
    highlightedFile,
    setSelectedFile,
    setFileContent,
    setShowFileContent,
    fetchFiles,
    setActiveFolder,
    needsRefresh,
    setNeedsRefresh
  } = useWorkspaceStore(useShallow(state => ({
    files: state.files,
    loading: state.loading,
    error: state.error,
    setError: state.setError,
    searchQuery: state.searchQuery,
    setSearchQuery: state.setSearchQuery,
    uploadDialog: state.uploadDialog,
    setUploadDialog: state.setUploadDialog,
    openUploadDialog: state.openUploadDialog,
    closeUploadDialog: state.closeUploadDialog,
    createFolderDialog: state.createFolderDialog,
    openCreateFolderDialog: state.openCreateFolderDialog,
    closeCreateFolderDialog: state.closeCreateFolderDialog,
    deleteDialog: state.deleteDialog,
    setDeleteDialog: state.setDeleteDialog,
    openDeleteDialog: state.openDeleteDialog,
    closeDeleteDialog: state.closeDeleteDialog,
    deleteAllFilesDialog: state.deleteAllFilesDialog,
    setDeleteAllFilesDialog: state.setDeleteAllFilesDialog,
    openDeleteAllFilesDialog: state.openDeleteAllFilesDialog,
    closeDeleteAllFilesDialog: state.closeDeleteAllFilesDialog,
    showActionsDropdown: state.showActionsDropdown,
    setShowActionsDropdown: state.setShowActionsDropdown,
    expandedFolders: state.expandedFolders,
    setExpandedFolders: state.setExpandedFolders,
    expandFoldersToLevel: state.expandFoldersToLevel,
    toggleFolder: state.toggleFolder,
    highlightedFile: state.highlightedFile,
    setSelectedFile: state.setSelectedFile,
    setFileContent: state.setFileContent,
    setShowFileContent: state.setShowFileContent,
    fetchFiles: state.fetchFiles,
    setActiveFolder: state.setActiveFolder,
    needsRefresh: state.needsRefresh,
    setNeedsRefresh: state.setNeedsRefresh,
  })))

  const fetchCapabilities = useCapabilitiesStore(s => s.fetchCapabilities)

  // Move dialog state lives here (not in the store) because only this tree uses it.
  // We should rename the local one to avoid confusion or remove the one from store if unused
  // But for now, we'll keep the local one as it was added for the rename feature
  // and the store one might be used by other components or hooks

  // Local Move Dialog State (overrides store state for this component)
  const [localMoveDialog, setLocalMoveDialog] = useState<{
    isOpen: boolean
    isLoading: boolean
    item: PlannerFile | null
    destinationPath: string
    commitMessage: string
  }>({
    isOpen: false,
    isLoading: false,
    item: null,
    destinationPath: '',
    commitMessage: ''
  })
  const openLocalMoveDialog = (item: PlannerFile) => {
    setLocalMoveDialog({
      isOpen: true,
      isLoading: false,
      item,
      destinationPath: '', // Will be set in dialog
      commitMessage: ''
    })
  }
  const closeLocalMoveDialog = () => {
    setLocalMoveDialog({
      isOpen: false,
      isLoading: false,
      item: null,
      destinationPath: '',
      commitMessage: ''
    })
  }

  // Rename Dialog State
  const [renameDialog, setRenameDialog] = useState<{
    isOpen: boolean
    isLoading: boolean
    item: PlannerFile | null
  }>({
    isOpen: false,
    isLoading: false,
    item: null
  })

  const openRenameDialog = (item: PlannerFile) => {
    setRenameDialog({
      isOpen: true,
      isLoading: false,
      item
    })
  }

  const closeRenameDialog = () => {
    setRenameDialog({
      isOpen: false,
      isLoading: false,
      item: null
    })
  }

  // Ref for the workspace scrollable container
  const workspaceScrollRef = useRef<HTMLDivElement>(null)

  // Track which workflow preset we've already auto-expanded to prevent re-expansion
  const autoExpandedWorkflowRef = useRef<string | null>(null)
  // Track whether we've auto-expanded Chats/ for multi-agent mode
  const autoExpandedChatRef = useRef(false)
  // Track workflow folders that already have a full tree in memory
  const fullyLoadedWorkflowFoldersRef = useRef<Set<string>>(new Set())
  // Tracks which iterations have been lazy-loaded, keyed by workflowFolder
  const loadedIterationsRef = useRef<Map<string, Set<string>>>(new Map())
  // Get workflow folder path from selected workflow folder in the preset
  // The selectedFolder.filepath is the folder path stored in the database
  // We'll use this directly to filter the workspace
  const workflowFolderPath = useMemo(() => {
    if (activeWorkflowPreset?.selectedFolder?.filepath) {
      const filepath = activeWorkflowPreset.selectedFolder.filepath
      // The filepath from the database is the folder path (e.g., "Workflow/MyProject/" or "Workflow/MyProject")
      // If it ends with a file (has extension), get the parent folder
      const parts = filepath.split('/').filter(Boolean)
      if (parts.length > 0) {
        const lastPart = parts[parts.length - 1]
        const isFile = lastPart.includes('.')
        if (isFile && parts.length > 1) {
          // It's a file, get its parent folder
          return parts.slice(0, -1).join('/')
        } else {
          // It's already a folder - use it directly
          return parts.join('/')
        }
      }
    }
    return null
  }, [activeWorkflowPreset])

  const effectiveWorkflowFolderPath = useMemo(() => {
    if (selectedModeCategory !== 'workflow') return null
    if (showWorkflowsOverview || showSchedulesOverview) return null
    return workflowFolderPath
  }, [selectedModeCategory, showWorkflowsOverview, showSchedulesOverview, workflowFolderPath])

  const instructionSessionId = useChatStore(state => {
    const tabId = selectedModeCategory === 'workflow'
      ? activeWorkflowTabIdForPreset(state.activeTabId, activeWorkflowPreset?.id ?? null, state.chatTabs)
      : state.activeTabId
    const tab = tabId ? state.chatTabs[tabId] : undefined
    if (scopedWorkspacePath && tab?.metadata?.agentProfileWorkspace &&
        publicWorkspacePathForUser(scopedWorkspacePath, authUser?.id).replace(/\/+$/, '') !==
        publicWorkspacePathForUser(tab.metadata.agentProfileWorkspace, authUser?.id).replace(/\/+$/, '')) return undefined
    return tab?.sessionId
  })

  // Determine which folder to pass to the API based on mode
  const activeFolder = useMemo(() => {
    if (scopedWorkspacePath) return scopedWorkspacePath
    if (showWorkflowsOverview || showSchedulesOverview) return 'Workflow'
    if (selectedModeCategory === 'workflow') {
      if (effectiveWorkflowFolderPath) return effectiveWorkflowFolderPath
    }
    // For multi-agent mode and default, fetch root (Chats + skills are at root level)
    return undefined
  }, [scopedWorkspacePath, showWorkflowsOverview, showSchedulesOverview, selectedModeCategory, effectiveWorkflowFolderPath])

  // Raw workspace axios instance
  const wsRawApi = workspaceApi

  // Workspace file API
  const wsFileApi = useMemo(() => ({
    getFileContent: agentApi.getPlannerFileContent,
    updateFile: agentApi.updatePlannerFile,
    deleteFile: agentApi.deletePlannerFile,
    deleteFolder: agentApi.deletePlannerFolder,
    uploadFile: agentApi.uploadPlannerFile,
    createFolder: agentApi.createPlannerFolder,
    moveFile: agentApi.movePlannerFile,
    deleteAllFilesInFolder: agentApi.deleteAllFilesInFolder,
  }), [])

  // Helper function to apply filtering and path adjustment to files
  // This matches the logic in filteredFiles useMemo to ensure paths are consistent
  const applyFilteringAndPathAdjustment = useCallback((filesToProcess: PlannerFile[]): PlannerFile[] => {
    let result = filesToProcess
    const scopedHiddenRootFolders = hideManagedEntriesByDefault && showHiddenFiles ? [] : hiddenRootFolders

    // Only filter if we're in workflow mode and have a workflow folder path
    // When in multi-agent mode, show all files regardless of preset
    if (scopedWorkspacePath) {
      result = scopeFilesToWorkspace(result, scopedWorkspacePath, scopedHiddenRootFolders, authUser?.id)
    } else if (selectedModeCategory === 'workflow' && effectiveWorkflowFolderPath) {
      // Files are already scoped to the workflow folder by the API (folder param)
      // Just adjust filepaths to show workflow folder as root
      result = adjustFilePathsRecursive(result, effectiveWorkflowFolderPath)
    } else if (selectedModeCategory === 'multi-agent') {
      // Multi Agent Chat mode: show Chats/, Downloads/, pulse/, skills/, subagents/ and memories/ folders
      result = filesToProcess.filter(f => {
        const topFolder = f.filepath.split('/')[0]
        return topFolder === 'Chats' || topFolder === 'Downloads' || topFolder === 'pulse' || topFolder === 'skills' || topFolder === 'subagents' || topFolder === 'memories' || topFolder === 'chat_history' || topFolder === '_users'
      })
      // Scope _users/ to current user only — hide other users' folders
      result = result.map(f => {
        if (f.filepath === '_users' && f.children) {
          return { ...f, children: f.children.filter(c => c.filepath === currentUserFolder || c.filepath.startsWith(currentUserFolder + '/')) }
        }
        return f
      })
    }

    return result
  }, [scopedWorkspacePath, hiddenRootFolders, selectedModeCategory, effectiveWorkflowFolderPath, currentUserFolder, hideManagedEntriesByDefault, showHiddenFiles, authUser?.id])

  // Fetch capabilities on mount
  useEffect(() => {
    fetchCapabilities()
  }, [fetchCapabilities])

  // Restore expanded folders when files change
  // This runs after store's fetchFiles completes and handles workflow mode filtering
  useEffect(() => {
    // Skip if no files loaded yet
    if (files.length === 0) return

    // In workflow mode, completely skip restore effect to let auto-expand handle it
    // This prevents any interference with the auto-expansion logic
    if (selectedModeCategory === 'workflow' && effectiveWorkflowFolderPath) {
      return
    }

    // Get current expanded folders
    const currentExpanded = expandedFolders
    const previouslyExpanded = new Set(currentExpanded)

    // Apply filtering and path adjustment to get display files
    const displayFiles = applyFilteringAndPathAdjustment(files)

    // Collect all available folder paths from display files
    const availableFolderPaths = collectFolderPaths(displayFiles, true)

    // Restore expanded folders that still exist in the filtered tree
    const restoredExpanded = restoreExpandedFolders(previouslyExpanded, availableFolderPaths)

    // Only update if something changed AND we had folders expanded before
    // This prevents auto-collapse when files refresh during agent runs
    if (previouslyExpanded.size > 0 && restoredExpanded.size !== previouslyExpanded.size) {
      setExpandedFolders(restoredExpanded)
    }
  }, [files, applyFilteringAndPathAdjustment, expandedFolders, setExpandedFolders, selectedModeCategory, effectiveWorkflowFolderPath])

  // Function to scroll to highlighted file
  const scrollToHighlightedFile = useCallback((filepath: string) => {
    if (!workspaceScrollRef.current) return

    // Find the highlighted file element by looking for the data attribute or class
    // Check both data-filepath (adjusted path) and data-original-filepath (original path)
    // This ensures workspace tool events can scroll to files even when paths are adjusted in workflow mode
    const highlightedElement = workspaceScrollRef.current.querySelector(`[data-filepath="${filepath}"]`) ||
                              workspaceScrollRef.current.querySelector(`[data-original-filepath="${filepath}"]`) ||
                              workspaceScrollRef.current.querySelector(`[data-highlighted="true"]`)

    if (highlightedElement) {
      highlightedElement.scrollIntoView({
        behavior: 'smooth',
        block: 'center',
        inline: 'nearest'
      })
    }
  }, [])

  // Helper function to get the original filepath for API calls
  // Uses originalFilepath if available (when path was adjusted for display), otherwise uses filepath
  const getOriginalFilePath = useCallback((file: PlannerFile | string): string => {
    // If file is a string, it's a path string
    if (typeof file === 'string') {
      const filepath = file
      const pathScope = scopedWorkspacePath || (selectedModeCategory === 'workflow' ? effectiveWorkflowFolderPath : null)
      // If the panel is not scoped to a product or workflow folder, return as-is.
      if (!pathScope) {
        return filepath
      }

      // Check if the filepath has been adjusted (doesn't start with workflow folder path)
      // Use the utility function to reconstruct if needed
      if (!isPathWithinFolder(filepath, pathScope)) {
        return getOriginalPath(filepath, pathScope)
      }

      // If the filepath already starts with the workflow folder path, use it as-is
      return filepath
    }

    // If file is a PlannerFile object, use originalFilepath if available
    return file.originalFilepath || file.filepath
  }, [scopedWorkspacePath, selectedModeCategory, effectiveWorkflowFolderPath])

  // Legacy function name for backward compatibility
  const getFullFilePath = getOriginalFilePath

  // Recursively prune the runs/ subtree to only show the specified iteration
  const pruneRunsToIteration = useCallback(function pruneRuns(
    node: PlannerFile,
    iterationName: string,
  ): PlannerFile {
    if (node.filepath === 'runs' && node.children) {
      const matching = node.children.find(c =>
        c.filepath === `runs/${iterationName}` || c.filepath.endsWith(`/${iterationName}`)
      )
      return { ...node, children: matching ? [matching] : [] }
    }
    if (node.children && node.children.length > 0) {
      return { ...node, children: node.children.map(c => pruneRuns(c, iterationName)) }
    }
    return node
  }, [])

  // Filter by query: match file names and folder names; hide folders with no match and no matching descendants
  const filterFiles = (files: PlannerFile[], query: string): PlannerFile[] => {
    if (!query.trim()) return files

    const lowercaseQuery = query.toLowerCase()

    const filterRecursive = (fileList: PlannerFile[]): PlannerFile[] => {
      return fileList.map(file => {
        if (file.type === 'folder') {
          const filteredChildren = file.children ? filterRecursive(file.children) : []
          const folderName = file.filepath.split('/').filter(Boolean).pop() || file.filepath
          const folderMatches = folderName.toLowerCase().includes(lowercaseQuery) ||
            file.filepath.toLowerCase().includes(lowercaseQuery)
          const keepFolder = folderMatches || filteredChildren.length > 0
          if (!keepFolder) return null
          return {
            ...file,
            children: filteredChildren
          }
        } else {
          const fileName = file.filepath.split('/').pop() || file.filepath
          const matches = fileName.toLowerCase().includes(lowercaseQuery) ||
            file.filepath.toLowerCase().includes(lowercaseQuery)
          return matches ? file : null
        }
      }).filter(Boolean) as PlannerFile[]
    }

    return filterRecursive(files)
  }

  // Iteration display filter - Files defaults to iteration-0 for stable workflow views.
  // selectedRunFolder is still used by useIterationExpansion for execution context.
  const selectedRunFolder = useWorkflowStore(state => state.selectedRunFolder)
  const runFolders = useWorkflowStore(state => state.runFolders)

  // Reset loaded iteration cache when switching workflows so a stale folder fetch
  // from the previous workflow does not affect the next workflow.
  const prevActiveFolderForIterRef = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (prevActiveFolderForIterRef.current === activeFolder) return

    const previousActiveFolder = prevActiveFolderForIterRef.current
    prevActiveFolderForIterRef.current = activeFolder

    if (previousActiveFolder) {
      loadedIterationsRef.current.delete(previousActiveFolder)
    }
    if (activeFolder) {
      loadedIterationsRef.current.delete(activeFolder)
    }
  }, [activeFolder])

  // Compute available iterations from the raw file tree (populated even from shallow fetch)
  const availableIterations = useMemo(() => {
    if (selectedModeCategory !== 'workflow' || !effectiveWorkflowFolderPath) return []
    const discovered = new Set<string>()
    const workflowItem = files.find(f => f.filepath.replace(/\/$/, '') === effectiveWorkflowFolderPath)
    if (workflowItem?.children) {
      const runsFolder = workflowItem.children.find(c =>
        c.filepath === effectiveWorkflowFolderPath + '/runs' || c.filepath === effectiveWorkflowFolderPath + '/runs/'
      )
      runsFolder?.children
        ?.filter(c => /iteration-\d+/.test(c.filepath))
        .map(c => c.filepath.split('/').pop() ?? '')
        .filter(Boolean)
        .forEach(iter => discovered.add(iter))
    }

    runFolders
      .map(folder => folder.name)
      .filter(name => /^iteration-\d+(?:-(?:hook|sched|slack-[a-f0-9]+))?$/.test(name))
      .forEach(iter => discovered.add(iter))

    return Array.from(discovered).sort((a, b) => {
      const numA = parseInt(a.match(/iteration-(\d+)/)?.[1] ?? '0')
      const numB = parseInt(b.match(/iteration-(\d+)/)?.[1] ?? '0')
      return numA - numB
    })
  }, [files, selectedModeCategory, effectiveWorkflowFolderPath, runFolders])

  const latestIteration = availableIterations[availableIterations.length - 1] ?? null
  const defaultWorkflowIteration = useMemo(() => {
    if (selectedModeCategory !== 'workflow') return null
    return availableIterations.includes('iteration-0') ? 'iteration-0' : latestIteration
  }, [selectedModeCategory, availableIterations, latestIteration])

  const effectiveDisplayedIteration = defaultWorkflowIteration

  // Get filtered files - first filter to workflow folder if preset is active, then apply search
  const filteredFiles = useMemo(() => {
    let result = files
    const scopedHiddenRootFolders = hideManagedEntriesByDefault && showHiddenFiles ? [] : hiddenRootFolders

    // Only filter if we're in workflow mode and have a workflow folder path
    // When in multi-agent mode, show all files regardless of preset
    if (scopedWorkspacePath) {
      result = scopeFilesToWorkspace(result, scopedWorkspacePath, scopedHiddenRootFolders, authUser?.id)
    } else if (selectedModeCategory === 'workflow' && effectiveWorkflowFolderPath) {
      // The API returns the workflow folder itself AND its children as flat top-level siblings.
      // e.g., [Workflow/codeanalysis, Workflow/codeanalysis/knowledgebase, Workflow/codeanalysis/learnings, ...]
      // The folder item (Workflow/codeanalysis) already has children nested inside it.
      // If we adjust all flat items, we get duplicates: the folder's children appear BOTH
      // inside the folder AND as top-level siblings. Fix: find the workflow folder item
      // and use only it (with its nested children) as the root of the tree.
      const workflowFolderItem = result.find(f => {
        const normalized = f.filepath.replace(/\/$/, '')
        const normalizedTarget = effectiveWorkflowFolderPath.replace(/\/$/, '')
        return normalized === normalizedTarget
      })
      if (workflowFolderItem && workflowFolderItem.children && workflowFolderItem.children.length > 0) {
        // Use the folder as a single root with its proper nested children.
        // Also include root-level files that are siblings of the folder item
        // (e.g., workflow.json appears as a separate data[] entry, not inside children)
        const rootPrefix = effectiveWorkflowFolderPath.replace(/\/$/, '') + '/'
        const rootLevelFiles = result.filter(f =>
          f.type !== 'folder' &&
          f.filepath.startsWith(rootPrefix) &&
          !f.filepath.slice(rootPrefix.length).includes('/')
        )
        // Create a copy with merged children (don't mutate zustand state)
        const mergedFolder = {
          ...workflowFolderItem,
          children: [...workflowFolderItem.children, ...rootLevelFiles]
        }
        result = adjustFilePathsRecursive([mergedFolder], effectiveWorkflowFolderPath)
      } else {
        // Fallback: no hierarchical folder found, adjust all items
        result = adjustFilePathsRecursive(result, effectiveWorkflowFolderPath)
      }

      // Filter runs/ to only show the selected/latest iteration (not all iterations)
      if (effectiveDisplayedIteration) {
        result = result.map(node => pruneRunsToIteration(node, effectiveDisplayedIteration))
      }
    } else if (selectedModeCategory === 'multi-agent') {
      // Multi Agent Chat mode: show Chats/, Downloads/, pulse/, skills/, subagents/ and memories/ folders
      result = files.filter(f => {
        const topFolder = f.filepath.split('/')[0]
        return topFolder === 'Chats' || topFolder === 'Downloads' || topFolder === 'pulse' || topFolder === 'skills' || topFolder === 'subagents' || topFolder === 'memories' || topFolder === 'chat_history' || topFolder === '_users'
      })
      // Scope _users/ to current user only — hide other users' folders
      result = result.map(f => {
        if (f.filepath === '_users' && f.children) {
          return { ...f, children: f.children.filter(c => c.filepath === currentUserFolder || c.filepath.startsWith(currentUserFolder + '/')) }
        }
        return f
      })
    }

    if (hideManagedEntriesByDefault && !showHiddenFiles) {
      result = hideManagedWorkspaceRootEntries(result, hiddenRootFolders)
    }

    // Apply search filter
    result = filterFiles(result, searchQuery)

    return result
  }, [files, scopedWorkspacePath, hiddenRootFolders, effectiveWorkflowFolderPath, searchQuery, selectedModeCategory, effectiveDisplayedIteration, currentUserFolder, pruneRunsToIteration, hideManagedEntriesByDefault, showHiddenFiles, authUser?.id])

  // Reveal search matches by opening the folders on their path, without permanently
  // forcing every folder open: only newly-matched folders get expanded, so a folder
  // the user explicitly collapses stays collapsed as they keep refining the query.
  const searchAutoExpandedRef = useRef<Set<string>>(new Set())
  useEffect(() => {
    if (!searchQuery.trim()) {
      searchAutoExpandedRef.current = new Set()
      return
    }
    const matchedFolderPaths = new Set<string>()
    const collectFolders = (list: PlannerFile[]) => {
      for (const file of list) {
        if (file.type === 'folder') {
          matchedFolderPaths.add(file.filepath)
          if (file.children?.length) collectFolders(file.children)
        }
      }
    }
    collectFolders(filteredFiles)

    const newlyMatched = [...matchedFolderPaths].filter(path => !searchAutoExpandedRef.current.has(path))
    matchedFolderPaths.forEach(path => searchAutoExpandedRef.current.add(path))
    if (newlyMatched.length === 0) return
    setExpandedFolders(new Set([...expandedFolders, ...newlyMatched]))
  }, [searchQuery, filteredFiles, expandedFolders, setExpandedFolders])

  // Refresh file tree from server (re-fetch all files so local filter can find them)
  const handleRefreshAndSearch = useCallback(async () => {
    setServerSearchLoading(true)
    try {
      await fetchFiles(
        activeFolder ?? undefined,
        activeFolder || selectedModeCategory === 'workflow'
          ? { force: true }
          : { force: true, maxDepth: 2 }
      )
    } catch (err) {
      console.error('Failed to refresh files:', err)
    } finally {
      setServerSearchLoading(false)
    }
  }, [activeFolder, fetchFiles, selectedModeCategory])

  // File highlighting with auto-scroll only — we deliberately do NOT call
  // expandFoldersForFile() here in any mode. It opens every ancestor folder with
  // no depth limit, and since highlights fire repeatedly over a session (each new
  // tool-created or updated file), that steadily accumulates into "everything is
  // expanded" — the opposite of the first-level-only default. The relevant
  // top-level folders are already open from the auto-expand effects; deeper ones
  // stay collapsed until the user opens them.
  useEffect(() => {
    if (highlightedFile) {
      // Auto-scroll to highlighted file after a short delay
      setTimeout(() => {
        scrollToHighlightedFile(highlightedFile)
      }, 100)
    }
  }, [highlightedFile, scrollToHighlightedFile])

  // Automatically expand workspace folder when a workflow is first opened
  // Only runs once per workflow preset or mode switch to allow manual open/close afterward
  useEffect(() => {
    const workspaceScope = scopedWorkspacePath || (selectedModeCategory === 'workflow' ? effectiveWorkflowFolderPath : null)
    if (workspaceScope && filteredFiles.length > 0) {
      // Check if we've already auto-expanded for this workflow preset
      const workflowPresetId = scopedWorkspacePath || activeWorkflowPreset?.id || effectiveWorkflowFolderPath

      // Only auto-expand if we haven't done it for this workflow yet
      if (autoExpandedWorkflowRef.current !== workflowPresetId) {
        // Small delay to ensure files are fully loaded and rendered
        const timeoutId = setTimeout(() => {
          // Determine what files to expand
          const workflowFolder = filteredFiles.length > 0 && filteredFiles[0].type === 'folder' ? filteredFiles[0] : null
          const filesToExpand = workflowFolder && workflowFolder.children
            ? workflowFolder.children
            : filteredFiles

          if (expandFirstLevelFolders) {
            // filesToExpand is already workflowFolder.children, so level 0 means
            // direct children only.
            const additionalFolders = workflowFolder ? [workflowFolder.filepath] : undefined
            const excludeFolders = scopedWorkspacePath ? [] : ['planning', 'variables', 'learnings', 'logs', 'runs']
            expandFoldersToLevel(filesToExpand, 0, additionalFolders, excludeFolders)
          } else {
            // Across every product, keep the workspace root open while every
            // direct child folder starts closed.
            setExpandedFolders(getInitialExpandedWorkspaceFolders(workflowFolder))
          }

          // Mark this workflow as auto-expanded
          autoExpandedWorkflowRef.current = workflowPresetId
        }, 500)

        return () => clearTimeout(timeoutId)
      }
    } else if (!scopedWorkspacePath && selectedModeCategory !== 'workflow') {
      // Reset the auto-expanded ref when switching away
      autoExpandedWorkflowRef.current = null
    }

  }, [scopedWorkspacePath, selectedModeCategory, effectiveWorkflowFolderPath, filteredFiles, expandFoldersToLevel, setExpandedFolders, expandFirstLevelFolders, activeWorkflowPreset?.id])

  // In multi-agent mode, auto-expand Chats/ folder by default (skills/ stays closed)
  useEffect(() => {
    if (!scopedWorkspacePath && selectedModeCategory === 'multi-agent' && filteredFiles.length > 0 && !autoExpandedChatRef.current) {
      const hasChatsFolder = filteredFiles.some(f => f.filepath === 'Chats' || f.filepath === 'Chats/')
      if (hasChatsFolder) {
        autoExpandedChatRef.current = true
        setExpandedFolders(new Set(['Chats']))
      }
    } else if (selectedModeCategory !== 'multi-agent') {
      autoExpandedChatRef.current = false
    }
  }, [scopedWorkspacePath, selectedModeCategory, filteredFiles, setExpandedFolders])

  // Use custom hook to handle iteration expansion logic
  useIterationExpansion({
    selectedModeCategory,
    workflowFolderPath: effectiveWorkflowFolderPath,
    filteredFiles,
    selectedRunFolder,
    expandedFolders,
    setExpandedFolders
  })

  // Lazy-load the selected/latest iteration's full files when it changes
  useEffect(() => {
    if (selectedModeCategory !== 'workflow' || !activeFolder || !effectiveDisplayedIteration) return
    if (fullyLoadedWorkflowFoldersRef.current.has(activeFolder)) return
    if (!availableIterations.includes(effectiveDisplayedIteration)) return

    const loaded = loadedIterationsRef.current.get(activeFolder)
    if (loaded?.has(effectiveDisplayedIteration)) return // Already fetched this session

    if (!loadedIterationsRef.current.has(activeFolder)) {
      loadedIterationsRef.current.set(activeFolder, new Set())
    }
    loadedIterationsRef.current.get(activeFolder)!.add(effectiveDisplayedIteration)
    fetchFiles(activeFolder + '/runs/' + effectiveDisplayedIteration).catch(() => {
      loadedIterationsRef.current.get(activeFolder)?.delete(effectiveDisplayedIteration)
    })
  }, [selectedModeCategory, activeFolder, effectiveDisplayedIteration, availableIterations, fetchFiles])

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      const target = event.target as Element
      if (showActionsDropdown && !target.closest('.actions-dropdown')) {
        setShowActionsDropdown(false)
      }
    }

    document.addEventListener('mousedown', handleClickOutside)
    return () => {
      document.removeEventListener('mousedown', handleClickOutside)
    }
  }, [showActionsDropdown, setShowActionsDropdown])

  // Load files on component mount and re-fetch when active folder changes.
  // Keep the store's activeFolder in sync with the workspace scope.
  // This ensures that unscoped fetchFiles() calls from other components (StepNode, WorkflowCanvas,
  // etc.) stay scoped to the workflow folder instead of fetching the entire root tree.
  useEffect(() => {
    setActiveFolder(activeFolder ?? null)
  }, [activeFolder, setActiveFolder])

  // Workflow folders now prefer the full tree on first load so the files panel is complete immediately.
  useEffect(() => {
    // PERF: Use getState() to avoid re-running this effect when fetchFiles reference changes.
    // fetchFiles is a zustand store function — its reference changes on every store update,
    // which would cause this effect to re-run and re-fetch on every render.
    const { fetchFiles: fetch } = useWorkspaceStore.getState()
    if (selectedModeCategory === 'workflow' || scopedWorkspacePath) {
      const targetFolder = activeFolder
      const shouldForceInitialWorkflowLoad = !!targetFolder && !fullyLoadedWorkflowFoldersRef.current.has(targetFolder)
      fetch(targetFolder, shouldForceInitialWorkflowLoad ? { force: true } : undefined)
        .then(() => {
          if (targetFolder) {
            fullyLoadedWorkflowFoldersRef.current.add(targetFolder)
          }
        })
        .catch(() => {
          if (targetFolder) {
            fullyLoadedWorkflowFoldersRef.current.delete(targetFolder)
          }
        })
    } else {
      fetch(activeFolder, { maxDepth: 2 })
    }

  }, [activeFolder, selectedModeCategory, scopedWorkspacePath])

  // Handle file click - load it into the shared viewer
  const handleFileClick = async (file: PlannerFile) => {
    if (file.type === 'folder') return
    // Reconstruct the original full path if we're in workflow mode with filtered files
    await openWorkspaceFile(getOriginalFilePath(file))
  }

  // Handle folder click - only folders are clickable now
  // Folders the server confirmed hold nothing. The tree gives every folder an empty child list, loaded or not, and the
  // first load stops two levels down, so "no children" alone cannot show a folder is empty. The first expand of such
  // a folder asks the server for just that folder; a tree reload drops the answers, and the next expand asks again.
  const [emptyFolders, setEmptyFolders] = useState<ReadonlySet<string>>(() => new Set())
  const checkingFolders = useRef(new Set<string>())
  useEffect(() => { setEmptyFolders(current => current.size === 0 ? current : new Set()) }, [files])
  const verifyFolderEmpty = useCallback(async (folder: PlannerFile) => {
    const apiPath = getOriginalFilePath(folder)
    checkingFolders.current.add(folder.filepath)
    try {
      const response = await agentApi.getPlannerFiles(apiPath, -1, 1)
      if (!response?.success) return
      const entries: PlannerFile[] = []
      const collect = (nodes: PlannerFile[]) => { for (const node of nodes) { if (node.filepath !== apiPath && node.filepath !== folder.filepath) entries.push(node); if (node.children?.length) collect(node.children) } }
      collect(Array.isArray(response.data) ? response.data : [])
      if (entries.length === 0) setEmptyFolders(current => new Set(current).add(folder.filepath))
    } catch {
      // Unknown stays unknown: the arrow remains.
    } finally {
      checkingFolders.current.delete(folder.filepath)
    }
  }, [getOriginalFilePath])
  const handleFolderClick = (folder: PlannerFile) => {
    if (folder.type !== 'folder') return
    const hasChildren = !!folder.children?.length
    if (!hasChildren && emptyFolders.has(folder.filepath)) return // known empty: nothing to open
    const expanding = !expandedFolders.has(folder.filepath)
    toggleFolder(folder.filepath)
    if (expanding && !hasChildren && !isSelectionMode && !checkingFolders.current.has(folder.filepath)) void verifyFolderEmpty(folder)
  }

  // Handle file delete
  const handleFileDelete = (file: PlannerFile) => {
    if (isProtectedWorkspaceEntry(file, protectedRootPath)) {
      setError('The workspace root and project metadata cannot be deleted from Files. Use the project delete action to remove the workspace.')
      return
    }
    openDeleteDialog(file)
  }

  // Handle folder delete
  const handleFolderDelete = (folder: PlannerFile) => {
    handleFileDelete(folder)
  }

  // Handle delete all contents in folder
  const handleDeleteAllFilesInFolder = (folder: PlannerFile) => {
    if (isProtectedWorkspaceEntry(folder, protectedRootPath)) {
      setError('Select the workspace contents to delete files. Use the project delete action to remove the workspace.')
      return
    }
    openDeleteAllFilesDialog(folder)
  }

  // Helper function to collect only top-level file paths (not recursive)
  // API handles recursive deletion automatically, so we only need to select top-level items
  const collectTopLevelFilePaths = useCallback((files: PlannerFile[]): string[] => {
    return workspaceSelectionItems(files, protectedRootPath).map(file => file.filepath)
  }, [protectedRootPath])

  // Toggle selection mode
  const toggleSelectionMode = useCallback(() => {
    setIsSelectionMode(prev => {
      if (!prev) {
        // Entering selection mode - clear any previous selections
        setSelectedFiles(new Set())
      } else {
        // Exiting selection mode - clear selections
        setSelectedFiles(new Set())
      }
      return !prev
    })
  }, [])

  // Toggle file selection - only select/unselect the item itself (not children)
  // API handles recursive deletion automatically, so we don't need to select children
  const toggleFileSelection = useCallback((file: PlannerFile) => {
    if (isProtectedWorkspaceEntry(file, protectedRootPath)) return
    setSelectedFiles(prev => {
      const newSet = new Set(prev)
      const filePath = file.filepath

      if (newSet.has(filePath)) {
        // Unselect: remove this item only (not children)
        newSet.delete(filePath)
      } else {
        // Select: add this item only (not children)
        newSet.add(filePath)
      }
      return newSet
    })
  }, [protectedRootPath])

  // Select file and enter selection mode
  // If it's a folder with children, enter selection mode without pre-selecting the folder itself
  // (to prevent accidentally deleting the entire folder when user only wants to select children)
  // If it's a file or leaf folder, pre-select it
  const selectFileAndEnterSelectionMode = useCallback((file: PlannerFile) => {
    if (isProtectedWorkspaceEntry(file, protectedRootPath)) return
    setIsSelectionMode(true)
    if (file.type === 'folder' && file.children && file.children.length > 0) {
      // Don't pre-select parent folders — user likely wants to select items inside
      setSelectedFiles(new Set())
    } else {
      setSelectedFiles(new Set([file.filepath]))
    }
  }, [protectedRootPath])

  // Select/Deselect all visible files (top-level only, not recursive)
  // API handles recursive deletion automatically, so we only need to select top-level items
  const toggleSelectAll = useCallback(() => {
    const allPaths = collectTopLevelFilePaths(filteredFiles)
    setSelectedFiles(prev => {
      const newSet = new Set(prev)
      // Check if all top-level items are selected
      const allSelected = allPaths.length > 0 && allPaths.every(path => newSet.has(path))
      if (allSelected) {
        // Deselect all top-level items
        allPaths.forEach(path => newSet.delete(path))
      } else {
        // Select all top-level items
        allPaths.forEach(path => newSet.add(path))
      }
      return newSet
    })
  }, [filteredFiles, collectTopLevelFilePaths])

  // Check if all top-level files are selected
  const areAllFilesSelected = useMemo(() => {
    if (selectedFiles.size === 0) return false
    const allPaths = collectTopLevelFilePaths(filteredFiles)
    return allPaths.length > 0 && allPaths.every(path => selectedFiles.has(path))
  }, [selectedFiles, filteredFiles, collectTopLevelFilePaths])

  // Get selected files as PlannerFile objects
  const getSelectedFilesAsObjects = useCallback((): PlannerFile[] => {
    const findFileByPath = (files: PlannerFile[], path: string): PlannerFile | null => {
      for (const file of files) {
        if (file.filepath === path) return file
        if (file.children) {
          const found = findFileByPath(file.children, path)
          if (found) return found
        }
      }
      return null
    }

    const selected: PlannerFile[] = []
    selectedFiles.forEach(path => {
      const file = findFileByPath(filteredFiles, path)
      if (file && !isProtectedWorkspaceEntry(file, protectedRootPath)) selected.push(file)
    })
    return selected
  }, [selectedFiles, filteredFiles, protectedRootPath])

  // Handle bulk delete
  const handleBulkDelete = useCallback(() => {
    const selectedItems = getSelectedFilesAsObjects()
    if (selectedItems.length === 0) return
    setBulkDeleteDialog({
      isOpen: true,
      isLoading: false,
      items: selectedItems
    })
  }, [getSelectedFilesAsObjects])

  // Confirm bulk delete
  const confirmBulkDelete = async () => {
    if (bulkDeleteDialog.items.length === 0) return

    setBulkDeleteDialog(prev => ({ ...prev, isLoading: true }))

    try {
      const errors: string[] = []

      // Deduplicate: if a parent folder is selected along with its children,
      // only delete the parent (folder deletion is recursive).
      // This prevents accidentally deleting more than intended.
      const allPaths = new Set(bulkDeleteDialog.items.map(item => getOriginalFilePath(item)))
      const itemsToDelete = bulkDeleteDialog.items.filter(item => {
        if (isProtectedWorkspaceEntry(item, protectedRootPath)) return false
        const itemPath = getOriginalFilePath(item)
        // Check if any ancestor of this item is also in the selection
        const parts = itemPath.split('/')
        for (let i = 1; i < parts.length; i++) {
          const ancestorPath = parts.slice(0, i).join('/')
          if (allPaths.has(ancestorPath)) {
            // An ancestor folder is also selected — skip this item (parent will handle it)
            return false
          }
        }
        return true
      })

      // Delete each item sequentially
      for (const item of itemsToDelete) {
        try {
          const fullFilePath = getOriginalFilePath(item)
          if (item.type !== 'folder') {
            await wsFileApi.deleteFile(fullFilePath)
          } else {
            await wsFileApi.deleteFolder(fullFilePath)
          }
        } catch (err) {
          const fileName = item.filepath.split('/').pop() || item.filepath
          errors.push(`${fileName}: ${err instanceof Error ? err.message : 'Failed to delete'}`)
          console.error(`[BulkDelete] Failed to delete "${item.filepath}":`, err)
        }
      }

      // Refresh the file list
      await fetchFiles(activeFolder, { force: true })

      // Clear selection and exit selection mode
      setSelectedFiles(new Set())
      setIsSelectionMode(false)

      // Close dialog
      setBulkDeleteDialog({ isOpen: false, isLoading: false, items: [] })

      // Show errors if any
      if (errors.length > 0) {
        setError(`Some files could not be deleted:\n${errors.join('\n')}`)
      }
    } catch (err) {
      console.error('Failed to delete items:', err)
      setError(err instanceof Error ? err.message : 'Failed to delete items')
      setBulkDeleteDialog(prev => ({ ...prev, isLoading: false }))
    }
  }

  // Cancel bulk delete
  const cancelBulkDelete = () => {
    setBulkDeleteDialog({ isOpen: false, isLoading: false, items: [] })
  }

  // Confirm delete
  const confirmDelete = async () => {
    if (!deleteDialog.item) return
    if (isProtectedWorkspaceEntry(deleteDialog.item, protectedRootPath)) return

    setDeleteDialog({ isLoading: true })

    try {
      // Use original filepath if available (when path was adjusted for display)
      const fullFilePath = getOriginalFilePath(deleteDialog.item)

      // Extract parent folder path to preserve its expanded state
      const itemPath = deleteDialog.item.filepath // Use display path for expanded folders
      const pathParts = itemPath.split('/').filter(Boolean)
      let parentFolderPath: string | null = null

      if (pathParts.length > 1) {
        // Parent folder is all parts except the last one
        parentFolderPath = pathParts.slice(0, -1).join('/')
      } else if (pathParts.length === 1) {
        // Item is at root level, no parent folder
        parentFolderPath = null
      }

      // Preserve parent folder expansion before refresh
      if (parentFolderPath) {
        const currentExpanded = useWorkspaceStore.getState().expandedFolders
        const newExpanded = new Set(currentExpanded)
        newExpanded.add(parentFolderPath)
        setExpandedFolders(newExpanded)
      }

      if (deleteDialog.item.type !== 'folder') {
        await wsFileApi.deleteFile(fullFilePath)
      } else {
        await wsFileApi.deleteFolder(fullFilePath)
      }

      // Refresh the file list to show updated state
      await fetchFiles(activeFolder, { force: true })

      // Close dialog
      closeDeleteDialog()
    } catch (err) {
      console.error('Failed to delete item:', err)
      setError(err instanceof Error ? err.message : 'Failed to delete item')
      setDeleteDialog({ isLoading: false })
    }
  }

  // Cancel delete
  const cancelDelete = () => {
    closeDeleteDialog()
  }

  // Confirm delete all contents
  const confirmDeleteAllFiles = async () => {
    if (!deleteAllFilesDialog.folder) return
    if (isProtectedWorkspaceEntry(deleteAllFilesDialog.folder, protectedRootPath)) return

    setDeleteAllFilesDialog({ isLoading: true })

    try {
      // Use original filepath if available (when path was adjusted for display)
      const fullFolderPath = getOriginalFilePath(deleteAllFilesDialog.folder)

      await wsFileApi.deleteAllFilesInFolder(fullFolderPath)

      // Refresh the file list to show updated state
      await fetchFiles(activeFolder, { force: true })

      // Close dialog
      closeDeleteAllFilesDialog()
    } catch (err) {
      console.error('Failed to delete all contents:', err)
      setError(err instanceof Error ? err.message : 'Failed to delete all contents')
      setDeleteAllFilesDialog({ isLoading: false })
    }
  }

  // Cancel delete all contents
  const cancelDeleteAllFiles = () => {
    closeDeleteAllFilesDialog()
  }

  // Handle file download
  const handleFileDownload = async (file: PlannerFile) => {
    let fullFilePath = ''
    try {
      // Use original filepath if available (when path was adjusted for display)
      fullFilePath = getOriginalFilePath(file)
      const fileName = fullFilePath.split('/').pop() || fullFilePath
      const extension = fileName.split('.').pop()?.toLowerCase() || ''
      setDownloadStatus({ path: fullFilePath, name: fileName, loaded: 0 })

      // List of binary file extensions
      const binaryExtensions = ['xls', 'xlsx', 'pdf', 'doc', 'docx', 'ppt', 'pptx', 'zip', 'rar', '7z', 'tar', 'gz', 'exe', 'dll', 'so', 'dylib', 'bin', 'qif', 'webm', 'mp4', 'mov']
      const isLikelyBinary = binaryExtensions.includes(extension)

      let blob: Blob
      let mimeType: string

      // For binary files, fetch directly as blob from workspace API with download parameter
      if (isLikelyBinary) {
        // Use workspaceApi (same as other workspace operations) with blob response type
        // IMPORTANT: responseType must be 'blob' to prevent axios from parsing JSON
        // Also set Accept header to request binary content
        const response = await wsRawApi.get(`/api/documents/${encodeURIComponent(fullFilePath)}`, {
          params: { download: 'true' },
          responseType: 'blob',
          headers: {
            'Accept': 'application/octet-stream'
          },
          onDownloadProgress: (progress) => {
            setDownloadStatus({
              path: fullFilePath,
              name: fileName,
              loaded: progress.loaded,
              total: progress.total ?? undefined,
            })
          },
        })

        // Check if we got JSON instead of blob (backend might not have handled download param correctly)
        if (response.data instanceof Blob) {
          // Check if it's actually JSON by reading the first few bytes
          const firstBytes = await response.data.slice(0, 10).arrayBuffer()
          const firstBytesArray = new Uint8Array(firstBytes)
          const firstChar = String.fromCharCode(firstBytesArray[0])

          // JSON typically starts with '{' or '['
          if (firstChar === '{' || firstChar === '[') {
            // It's JSON, read it and check
            const text = await response.data.text()
            try {
              const jsonData = JSON.parse(text)
              if (jsonData.success !== undefined) {
                // It's the API JSON response, not the binary file
                if (jsonData.data?.is_binary) {
                  throw new Error('Binary file download is not supported by this endpoint response. The API returned metadata instead of bytes.')
                }
                console.error('[Download] Server returned JSON instead of binary:', jsonData)
                throw new Error(`Server returned JSON instead of binary file. The download parameter may not be working correctly. Response: ${JSON.stringify(jsonData).substring(0, 200)}`)
              }
            } catch {
              // Not JSON, use as-is
            }
          }

          blob = response.data
          mimeType = blob.type || getMimeType(extension)
        } else {
          // Fallback: if we got something else, try to convert it
          mimeType = getMimeType(extension)
          blob = new Blob([response.data], { type: mimeType })
        }
      } else {
        // For text files and images, use the regular API
        const response = await wsFileApi.getFileContent(fullFilePath)

        if (!response.success || !response.data) {
          setError(response.message || 'Failed to download file')
          return
        }

        // If metadata says the file is binary even though the extension was not
        // in our local list, retry using the binary download endpoint.
        if (response.data.is_binary && !response.data.is_image) {
          // File is actually binary, use binary download path to get the actual file
          try {
            const binaryResponse = await wsRawApi.get(`/api/documents/${encodeURIComponent(fullFilePath)}`, {
              params: { download: 'true' },
              responseType: 'blob',
              headers: {
                'Accept': 'application/octet-stream'
              },
              onDownloadProgress: (progress) => {
                setDownloadStatus({
                  path: fullFilePath,
                  name: fileName,
                  loaded: progress.loaded,
                  total: progress.total ?? undefined,
                })
              },
              transformResponse: [(data) => data] // Prevent axios from parsing response
            })

            // Verify we got actual binary data, not JSON
            if (binaryResponse.data instanceof Blob) {
              // Check if it's actually JSON by reading the first few bytes
              const firstBytes = await binaryResponse.data.slice(0, 10).arrayBuffer()
              const firstBytesArray = new Uint8Array(firstBytes)
              const firstChar = String.fromCharCode(firstBytesArray[0])

              // JSON typically starts with '{' or '['
              if (firstChar === '{' || firstChar === '[') {
                // It's JSON, read it and check
                const text = await binaryResponse.data.text()
                try {
                  const jsonData = JSON.parse(text)
                  if (jsonData.success !== undefined) {
                    // It's the API JSON response, not the binary file
                    console.error('[Download] Server returned JSON instead of binary:', jsonData)
                    throw new Error(`Server returned JSON instead of binary file. The download parameter may not be working correctly.`)
                  }
                } catch {
                  // Not JSON, use as-is
                }
              }

              blob = binaryResponse.data
              mimeType = blob.type || getMimeType(extension)
            } else {
              mimeType = getMimeType(extension)
              blob = new Blob([binaryResponse.data], { type: mimeType })
            }
          } catch (binaryErr) {
            console.error('[Download] Failed to download binary file:', binaryErr)
            throw new Error('This file is binary but could not be downloaded. Please download it directly from the file system.')
          }

          // Skip the rest of the text/image handling and download immediately
          const url = URL.createObjectURL(blob)
          const link = document.createElement('a')
          link.href = url
          link.download = fileName
          document.body.appendChild(link)
          link.click()
          document.body.removeChild(link)
          URL.revokeObjectURL(url)
          return
        }

        // Handle different file types
        if (response.data.is_image && response.data.content.startsWith('data:image/')) {
          // Image file - convert base64 data URL to blob
          const base64Data = response.data.content.split(',')[1]
          const imageType = response.data.content.match(/data:image\/([^;]+)/)?.[1] || 'png'
          const binaryString = atob(base64Data)
          const bytes = new Uint8Array(binaryString.length)
          for (let i = 0; i < binaryString.length; i++) {
            bytes[i] = binaryString.charCodeAt(i)
          }
          blob = new Blob([bytes], { type: `image/${imageType}` })
          mimeType = `image/${imageType}`
        } else {
          // Text file - create blob from content
          const content = response.data.content
          mimeType = getMimeType(extension)
          blob = new Blob([content], { type: mimeType })
        }
      }

      // Create download link and trigger download
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = fileName
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
      URL.revokeObjectURL(url)
    } catch (err) {
      console.error('Failed to download file:', err)
      setError(err instanceof Error ? err.message : 'Failed to download file')
    } finally {
      setDownloadStatus((current) => current?.path === fullFilePath ? null : current)
    }
  }

  // Helper function to get MIME type from extension
  const getMimeType = (extension: string): string => {
    const mimeTypes: Record<string, string> = {
      'txt': 'text/plain',
      'json': 'application/json',
      'js': 'text/javascript',
      'ts': 'text/typescript',
      'tsx': 'text/typescript',
      'jsx': 'text/javascript',
      'html': 'text/html',
      'css': 'text/css',
      'md': 'text/markdown',
      'py': 'text/python',
      'go': 'text/plain',
      'yaml': 'text/yaml',
      'yml': 'text/yaml',
      'xml': 'text/xml',
      'csv': 'text/csv',
      'xls': 'application/vnd.ms-excel',
      'xlsx': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      'pdf': 'application/pdf',
      'webm': 'video/webm',
      'mp4': 'video/mp4',
      'mov': 'video/quicktime',
      'doc': 'application/msword',
      'docx': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      'ppt': 'application/vnd.ms-powerpoint',
      'pptx': 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
      'zip': 'application/zip',
      'rar': 'application/x-rar-compressed',
      '7z': 'application/x-7z-compressed',
      'tar': 'application/x-tar',
      'gz': 'application/gzip',
    }
    return mimeTypes[extension] || 'application/octet-stream'
  }

  // Confirm move
  const confirmMove = async (destinationPath: string, commitMessage?: string) => {
    if (!localMoveDialog.item) return

    setLocalMoveDialog(prev => ({ ...prev, isLoading: true }))

    try {
      // Use original filepath if available (when path was adjusted for display)
      const fullFilePath = getOriginalFilePath(localMoveDialog.item)

      // Reconstruct destination path using getFullFilePath to handle workflow mode correctly
      // This ensures paths like "HRMS PR Review/itemName" become "Workflow/HRMS PR Review/itemName"
      const fullDestinationPath = getFullFilePath(destinationPath)

      await wsFileApi.moveFile(fullFilePath, fullDestinationPath, commitMessage)

      // Refresh the file list to show updated state
      await fetchFiles(activeFolder, { force: true })

      // Update selected file if it was moved
      if (localMoveDialog.item.filepath === highlightedFile) {
        // Clear highlight since file moved
        setSelectedFile(null)
        setFileContent('')
        setShowFileContent(false)
      }

      // Close dialog
      closeLocalMoveDialog()
    } catch (err) {
      console.error('Failed to move item:', err)
      setError(err instanceof Error ? err.message : 'Failed to move item')
      setLocalMoveDialog(prev => ({ ...prev, isLoading: false }))
      throw err // Re-throw to let dialog handle the error
    }
  }

  // Cancel move
  const cancelMove = () => {
    closeLocalMoveDialog()
  }

  // Confirm rename
  const confirmRename = async (newName: string, commitMessage?: string) => {
    if (!renameDialog.item) return

    setRenameDialog(prev => ({ ...prev, isLoading: true }))

    try {
      // Use original filepath if available (when path was adjusted for display)
      const fullFilePath = getOriginalFilePath(renameDialog.item)

      // Calculate new path
      // Handle root level files correctly
      const pathParts = fullFilePath.split('/')
      const parentPath = pathParts.length > 1 ? pathParts.slice(0, -1).join('/') : ''
      const destinationPath = parentPath ? `${parentPath}/${newName}` : newName

      await wsFileApi.moveFile(fullFilePath, destinationPath, commitMessage)

      // Refresh the file list to show updated state
      await fetchFiles(activeFolder, { force: true })

      // Update selected file if it was renamed
      if (renameDialog.item.filepath === highlightedFile) {
        // Clear highlight since file renamed (or maybe we should update it to new name?)
        // For now, let's just clear selection to avoid errors
        setSelectedFile(null)
        setFileContent('')
        setShowFileContent(false)
      }

      // Close dialog
      closeRenameDialog()
    } catch (err) {
      console.error('Failed to rename item:', err)
      setError(err instanceof Error ? err.message : 'Failed to rename item')
      setRenameDialog(prev => ({ ...prev, isLoading: false }))
      throw err // Re-throw to let dialog handle the error
    }
  }

  // Handle file rename
  const handleFileRename = (file: PlannerFile) => {
    openRenameDialog(file)
  }

  // Handle folder rename
  const handleFolderRename = (folder: PlannerFile) => {
    openRenameDialog(folder)
  }

  // Handle file move
  const handleFileMove = (file: PlannerFile) => {
    openLocalMoveDialog(file)
  }

  // Handle folder move
  const handleFolderMove = (folder: PlannerFile) => {
    openLocalMoveDialog(folder)
  }

  // Upload functionality
  const handleUploadClick = () => {
    openUploadDialog(resolveWorkspaceUploadPath('/', scopedWorkspacePath || effectiveWorkflowFolderPath))
  }

  // Upload to specific folder
  const handleFolderUploadClick = (folderPath: string) => {
    // For string paths, use legacy reconstruction (backward compatibility)
    const fullFolderPath = getFullFilePath(folderPath)
    openUploadDialog(fullFolderPath)
  }

  const blockedExtensions = useMemo(() => [
    'exe', 'dll', 'so', 'dylib', 'bin', 'msi', 'dmg', 'iso', 'jar', 'bat', 'cmd', 'com', 'scr'
  ], [])

  const validateFile = useCallback((file: File): string | null => {
    const fileExt = file.name.split('.').pop()?.toLowerCase() || ''
    if (blockedExtensions.includes(fileExt)) {
      return 'Blocked file type (executable/system file)'
    }
    if (file.size > 5 * 1024 * 1024) {
      return 'File exceeds 5MB limit'
    }
    return null
  }, [blockedExtensions])

  const addFiles = useCallback((files: FileList | File[]) => {
    const newFiles = Array.from(files)
    setPendingFiles(prev => {
      const existingNames = new Set(prev.map(f => f.name))
      const unique = newFiles.filter(f => !existingNames.has(f.name))
      return [...prev, ...unique]
    })
    setUploadResults(null)
  }, [])

  const handleFileSelect = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const files = event.target.files
    if (!files || files.length === 0) return
    addFiles(files)
    // Reset input so the same files can be re-selected
    event.target.value = ''
  }, [addFiles])

  const removeFile = useCallback((index: number) => {
    setPendingFiles(prev => prev.filter((_, i) => i !== index))
  }, [])

  const handleUploadAll = useCallback(async () => {
    if (pendingFiles.length === 0) return

    setUploadDialog({ isLoading: true })
    setError(null)
    setUploadResults(null)

    const folderPath = resolveWorkspaceUploadPath(
      uploadDialog.folderPath || '/',
      scopedWorkspacePath || effectiveWorkflowFolderPath,
    )
    const results: { name: string; success: boolean; error?: string }[] = []

    for (let i = 0; i < pendingFiles.length; i++) {
      const file = pendingFiles[i]
      setUploadProgress({ current: i + 1, total: pendingFiles.length })

      const validationError = validateFile(file)
      if (validationError) {
        results.push({ name: file.name, success: false, error: validationError })
        continue
      }

      try {
        const commitMessage = uploadDialog.commitMessage || `Upload ${file.name}`
        await wsFileApi.uploadFile(file, folderPath, commitMessage)
        results.push({ name: file.name, success: true })
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'Upload failed'
        results.push({ name: file.name, success: false, error: msg })
      }
    }

    setUploadProgress(null)
    setUploadResults(results)

    const allSucceeded = results.every(r => r.success)
    if (allSucceeded) {
      await fetchFiles(activeFolder, { force: true })
      setPendingFiles([])
      closeUploadDialog()
      setUploadResults(null)
    } else {
      await fetchFiles(activeFolder, { force: true })
      // Keep dialog open to show results; remove succeeded files from pending
      const failedNames = new Set(results.filter(r => !r.success).map(r => r.name))
      setPendingFiles(prev => prev.filter(f => failedNames.has(f.name)))
      setUploadDialog({ isLoading: false })
    }
  }, [pendingFiles, uploadDialog.folderPath, uploadDialog.commitMessage, setUploadDialog, closeUploadDialog, fetchFiles, activeFolder, setError, scopedWorkspacePath, effectiveWorkflowFolderPath, validateFile, wsFileApi])

  const cancelUpload = useCallback(() => {
    setPendingFiles([])
    setUploadResults(null)
    setUploadProgress(null)
    setIsDragOver(false)
    closeUploadDialog()
  }, [closeUploadDialog])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDragOver(true)
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDragOver(false)
  }, [])

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDragOver(false)
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      addFiles(e.dataTransfer.files)
    }
  }, [addFiles])

  const formatFileSize = useCallback((bytes: number) => {
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  }, [])

  // Keyboard shortcuts for upload dialog
  useEffect(() => {
    if (!uploadDialog.isOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        if (!uploadDialog.isLoading) {
          cancelUpload()
        }
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [uploadDialog.isOpen, uploadDialog.isLoading, cancelUpload])

  // Folder creation handlers
  const handleCreateFolder = (parentFolder?: PlannerFile | string) => {
    // Use originalFilepath if parentFolder is a PlannerFile object, otherwise reconstruct from string
    const fullParentPath = parentFolder
      ? (typeof parentFolder === 'string'
          ? getFullFilePath(parentFolder)
          : getOriginalFilePath(parentFolder))
      : undefined
    openCreateFolderDialog(fullParentPath)
  }

  const handleCreateFolderSubmit = async (folderPath: string, commitMessage?: string) => {
    try {
      // Reconstruct folder path using getFullFilePath to handle workflow mode correctly
      // This ensures paths are correct even if parentPath wasn't properly fixed
      const fullFolderPath = getFullFilePath(folderPath)
      await wsFileApi.createFolder(fullFolderPath, commitMessage)

      // Refresh file list to show the new folder
      await fetchFiles(activeFolder, { force: true })

      // Close dialog
      closeCreateFolderDialog()
    } catch (err) {
      console.error('Failed to create folder:', err)
      throw err // Re-throw to let the dialog handle the error
    }
  }

  const cancelCreateFolder = () => {
    closeCreateFolderDialog()
  }

  return (
    <TooltipProvider>
      <div data-tour="workspace-open" data-testid="workspace-panel" className="flex flex-col h-full bg-background">
      {/* Header: VS Code-style Explorer */}
        <ExplorerHeader
          title={title}
          titleAction={headerAction}
          leading={isSelectionMode ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <label className="relative flex cursor-pointer items-center px-1">
                  <input type="checkbox" aria-label="Select all files" checked={areAllFilesSelected} onChange={toggleSelectAll} className="h-4 w-4 accent-primary" />
                </label>
              </TooltipTrigger>
              <TooltipContent><p>Select All</p></TooltipContent>
            </Tooltip>
          ) : undefined}
          toolbar={isSelectionMode ? (
            <>
              {selectedFiles.size > 0 && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button onClick={handleBulkDelete} disabled={loading || bulkDeleteDialog.isLoading} aria-label="Delete selected files" className="relative rounded p-1 text-destructive hover:bg-muted disabled:opacity-50">
                      <Trash2 className="h-4 w-4" />
                      <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-medium leading-4 text-destructive-foreground">{selectedFiles.size}</span>
                    </button>
                  </TooltipTrigger>
                  <TooltipContent><p>Delete {selectedFiles.size} selected file{selectedFiles.size !== 1 ? 's' : ''}</p></TooltipContent>
                </Tooltip>
              )}
              <Tooltip>
                <TooltipTrigger asChild>
                  <button onClick={toggleSelectionMode} aria-label="Exit selection mode" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><X className="h-4 w-4" /></button>
                </TooltipTrigger>
                <TooltipContent><p>Exit selection mode</p></TooltipContent>
              </Tooltip>
            </>
          ) : (
            <>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button onClick={handleUploadClick} disabled={loading} aria-label="Upload file" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><Upload className="h-4 w-4" /></button>
                </TooltipTrigger>
                <TooltipContent><p>Upload file</p></TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button onClick={() => handleCreateFolder()} disabled={loading} aria-label="New folder" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><FolderPlus className="h-4 w-4" /></button>
                </TooltipTrigger>
                <TooltipContent><p>New folder</p></TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button onClick={() => fetchFiles(activeFolder, { force: true })} disabled={loading} aria-label="Refresh files" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} /></button>
                </TooltipTrigger>
                <TooltipContent><p>Refresh</p></TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button onClick={() => setExpandedFolders(new Set())} aria-label="Collapse all folders" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><ChevronsDownUp className="h-4 w-4" /></button>
                </TooltipTrigger>
                <TooltipContent><p>Collapse all</p></TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button onClick={toggleSelectionMode} aria-label="Select files" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><CheckSquare className="h-4 w-4" /></button>
                </TooltipTrigger>
                <TooltipContent><p>Select files</p></TooltipContent>
              </Tooltip>
              {hideManagedEntriesByDefault && canShowManagedFiles && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={() => setShowHiddenFiles(current => !current)}
                      aria-pressed={showHiddenFiles}
                      aria-label={showHiddenFiles ? 'Hide internal files' : 'Show hidden files'}
                      className={`rounded p-1 transition-colors hover:bg-muted ${showHiddenFiles ? 'text-primary' : 'text-muted-foreground hover:text-foreground'}`}
                    >
                      {showHiddenFiles ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                  </TooltipTrigger>
                  <TooltipContent><p>{showHiddenFiles ? 'Hide internal files' : 'Show hidden files'}</p></TooltipContent>
                </Tooltip>
              )}
            </>
          )}
        />

      {/* Content */}
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
          {/* Stale workspace banner */}
          {needsRefresh && (
            <div className="flex items-center justify-between px-2 py-1.5 bg-amber-500/10 border-b border-amber-500/20 text-xs">
              <span className="text-amber-700 dark:text-amber-400">Files may be out of date</span>
                  <button
                    onClick={() => {
                      setNeedsRefresh(false)
                      fetchFiles(activeFolder, { force: true })
                    }}
                    disabled={loading}
                    className="ml-2 px-2 py-0.5 rounded text-amber-700 dark:text-amber-400 hover:bg-amber-500/20 font-medium disabled:opacity-50"
              >
                Refresh
              </button>
            </div>
          )}
          {/* Search sits in the content, below the header line */}
          <div className="shrink-0 px-2 pt-2">
            <div className="relative">
              <div className="absolute inset-y-0 left-0 pl-2.5 flex items-center pointer-events-none">
                <Search className="h-3.5 w-3.5 text-muted-foreground" />
              </div>
              <input
                type="text"
                placeholder="Filter files"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="block w-full rounded-md border border-input bg-transparent py-1 pl-8 pr-8 text-xs leading-5 text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
              {searchQuery && (
                <div className="absolute inset-y-0 right-0 pr-3 flex items-center">
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <button
                        onClick={() => setSearchQuery('')}
                        className="text-muted-foreground hover:text-foreground"
                      >
                        <X className="h-4 w-4" />
                      </button>
                    </TooltipTrigger>
                    <TooltipContent>
                      <p>Clear search</p>
                    </TooltipContent>
                  </Tooltip>
                </div>
              )}
            </div>
          </div>
          {/* Folder Structure - Full Width */}
          <div ref={workspaceScrollRef} className="min-h-0 flex-1 overflow-y-auto">
            <div className="px-2 py-2">
              {downloadStatus ? (
                <div role="status" aria-live="polite" data-testid="workspace-download-progress" className="mb-3 rounded-md border border-border bg-muted px-3 py-2 text-xs text-foreground">
                  <div className="flex items-center gap-2">
                    <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin" />
                    <span className="min-w-0 flex-1 truncate font-medium">Downloading {downloadStatus.name}</span>
                    {downloadStatus.total && downloadStatus.total > 0 ? (
                      <span className="shrink-0 tabular-nums">{Math.min(100, Math.round((downloadStatus.loaded / downloadStatus.total) * 100))}%</span>
                    ) : null}
                  </div>
                  <p className="mt-1 pl-5 text-xs text-muted-foreground">
                    {formatTransferSize(downloadStatus.loaded)}{downloadStatus.total ? ` of ${formatTransferSize(downloadStatus.total)}` : ' received'}
                  </p>
                </div>
              ) : null}
              {showHiddenFiles && canShowManagedFiles && instructionSessionId && (!searchQuery.trim() || 'agents.md system prompt'.includes(searchQuery.trim().toLowerCase())) && (
                <SessionInstructions sessionId={instructionSessionId} />
              )}
              <PlannerFileList
                protectedRootPath={protectedRootPath}
                files={filteredFiles}
                loading={loading}
                error={error}
                onFolderClick={handleFolderClick}
                onFileClick={handleFileClick}
                onFileDelete={handleFileDelete}
                onFolderDelete={handleFolderDelete}
                onDeleteAllFilesInFolder={handleDeleteAllFilesInFolder}
                onRetry={() => fetchFiles(
                  activeFolder,
                  selectedModeCategory === 'workflow' ? { force: true } : { force: true, maxDepth: 2 }
                )}
                expandedFolders={expandedFolders}
                chatFileContext={chatFileContext}
                addFileToContext={addFileToContext}
                highlightedFile={highlightedFile}
                onFolderUpload={handleFolderUploadClick}
                onCreateFolder={handleCreateFolder}
                onFileMove={handleFileMove}
                onFolderMove={handleFolderMove}
                onFileRename={handleFileRename}
                onFolderRename={handleFolderRename}
                onFileDownload={handleFileDownload}
                downloadingFilePath={downloadStatus?.path}
                hideAddToChat={hideAddToChat || (selectedModeCategory === 'workflow' && !!effectiveWorkflowFolderPath)}
                hideRootActions={hideRootActions}
                isSelectionMode={isSelectionMode}
                selectedFiles={selectedFiles}
                onToggleFileSelection={toggleFileSelection}
                onSelectFileAndEnterSelectionMode={selectFileAndEnterSelectionMode}
                emptyFolders={emptyFolders}
                scrollContainerRef={workspaceScrollRef}
              />

              {/* Refresh from server when local search finds nothing */}
              {searchQuery.trim() && filteredFiles.length === 0 && (
                <div className="flex flex-col items-center gap-2 pt-2 pb-4">
                  <button
                    onClick={handleRefreshAndSearch}
                    disabled={serverSearchLoading}
                    className="flex items-center gap-1.5 px-3 py-1.5 text-xs text-muted-foreground border border-border hover:bg-muted hover:text-foreground rounded-md transition-colors disabled:opacity-50"
                  >
                    {serverSearchLoading ? (
                      <>
                        <Loader2 className="h-3 w-3 animate-spin" />
                        Refreshing files...
                      </>
                    ) : (
                      <>
                        <RefreshCw className="h-3 w-3" />
                        Refresh &amp; search
                      </>
                    )}
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>

      {/* Delete Confirmation Dialog */}
      <ConfirmationDialog
        isOpen={deleteDialog.isOpen}
        onClose={cancelDelete}
        onConfirm={confirmDelete}
        title={deleteDialog.item?.type === 'folder' ? 'Delete Folder' : 'Delete File'}
        message={`Are you sure you want to delete "${deleteDialog.item ? deleteDialog.item.filepath.split('/').pop() : 'this item'}"? This action cannot be undone.${
          deleteDialog.item?.type === 'folder' ? ' This will delete all files and subfolders inside.' : ''
        }`}
        confirmText="Delete"
        cancelText="Cancel"
        type="danger"
        isLoading={deleteDialog.isLoading}
        ignoreWorkspaceAutoCollapse
      />

      {/* Delete All Files Confirmation Dialog */}
      <ConfirmationDialog
        isOpen={deleteAllFilesDialog.isOpen}
        onClose={cancelDeleteAllFiles}
        onConfirm={confirmDeleteAllFiles}
        title="Delete All Contents"
        message={`Are you sure you want to delete ALL CONTENTS (files and folders) in "${deleteAllFilesDialog.folder ? deleteAllFilesDialog.folder.filepath.split('/').pop() : 'this folder'}"? This action cannot be undone. The folder itself will remain, but all files and subfolders inside will be permanently deleted.`}
        confirmText="Delete All Contents"
        cancelText="Cancel"
        type="warning"
        isLoading={deleteAllFilesDialog.isLoading}
        ignoreWorkspaceAutoCollapse
        requireText={deleteAllFilesDialog.folder?.filepath.split('/').pop() || 'DELETE'}
      />

      {/* Bulk Delete Confirmation Dialog */}
      <ConfirmationDialog
        isOpen={bulkDeleteDialog.isOpen}
        onClose={cancelBulkDelete}
        onConfirm={confirmBulkDelete}
        title={`Delete ${bulkDeleteDialog.items.length} Item${bulkDeleteDialog.items.length !== 1 ? 's' : ''}`}
        message={`Are you sure you want to delete ${bulkDeleteDialog.items.length} item${bulkDeleteDialog.items.length !== 1 ? 's' : ''}? This action cannot be undone.\n\n${bulkDeleteDialog.items.slice(0, 10).map(item => {
          return `• ${item.filepath}${item.type === 'folder' ? ' (folder)' : ''}`
        }).join('\n')}${bulkDeleteDialog.items.length > 10 ? `\n... and ${bulkDeleteDialog.items.length - 10} more` : ''}`}
        confirmText={`Delete ${bulkDeleteDialog.items.length} Item${bulkDeleteDialog.items.length !== 1 ? 's' : ''}`}
        cancelText="Cancel"
        type="danger"
        isLoading={bulkDeleteDialog.isLoading}
        ignoreWorkspaceAutoCollapse
      />

      {/* Upload Dialog */}
      {uploadDialog.isOpen && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-card rounded-md border border-border p-6 w-full max-w-md mx-4 max-h-[80vh] flex flex-col shadow-md">
            <h3 className="text-sm font-semibold text-foreground mb-4">
              Upload Files
            </h3>

            <div className="space-y-4 overflow-y-auto flex-1 min-h-0">
              {/* Upload Destination Display */}
              <div>
                <label className="block text-sm font-medium text-foreground mb-1">
                  Upload Destination
                </label>
                <div className="px-3 py-2 bg-muted border border-border rounded-md">
                  <p className="text-sm text-foreground">
                    {uploadDialog.folderPath === '/' ? 'Root directory (/)' : uploadDialog.folderPath}
                  </p>
                </div>
              </div>

              {/* Commit Message Input */}
              <div>
                <label className="block text-sm font-medium text-foreground mb-1">
                  Commit Message (Optional)
                </label>
                <input
                  type="text"
                  value={uploadDialog.commitMessage}
                  onChange={(e) => setUploadDialog({ commitMessage: e.target.value })}
                  placeholder="Upload description"
                  disabled={uploadDialog.isLoading}
                  className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50"
                />
              </div>

              {/* Drag & Drop Zone */}
              <div>
                <label className="block text-sm font-medium text-foreground mb-1">
                  Select Files
                </label>
                <div
                  onDragOver={handleDragOver}
                  onDragLeave={handleDragLeave}
                  onDrop={handleDrop}
                  onClick={() => !uploadDialog.isLoading && fileInputRef.current?.click()}
                  className={`border-2 border-dashed rounded-md p-6 text-center cursor-pointer transition-colors ${
                    isDragOver
                      ? 'border-primary bg-primary/10'
                      : 'border-border hover:border-muted-foreground'
                  } ${uploadDialog.isLoading ? 'opacity-50 pointer-events-none' : ''}`}
                >
                  <Upload className="mx-auto h-8 w-8 text-muted-foreground mb-2" />
                  <p className="text-sm text-muted-foreground">
                    Drag files here or click to browse
                  </p>
                  <p className="text-xs text-muted-foreground mt-1">
                    Any files, 5MB max each
                  </p>
                </div>
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  onChange={handleFileSelect}
                  className="hidden"
                />
              </div>

              {/* Selected Files List */}
              {pendingFiles.length > 0 && (
                <div>
                  <label className="block text-sm font-medium text-foreground mb-1">
                    {pendingFiles.length} file{pendingFiles.length !== 1 ? 's' : ''} selected
                  </label>
                  <div className="border border-border rounded-md divide-y divide-border max-h-40 overflow-y-auto">
                    {pendingFiles.map((file, index) => {
                      const validationError = validateFile(file)
                      return (
                        <div key={`${file.name}-${index}`} className="flex items-center justify-between px-3 py-2 text-sm">
                          <div className="flex-1 min-w-0 mr-2">
                            <p className={`truncate ${validationError ? 'text-destructive' : 'text-foreground'}`}>
                              {file.name}
                            </p>
                            {validationError ? (
                              <p className="text-xs text-destructive">{validationError}</p>
                            ) : (
                              <p className="text-xs text-muted-foreground">{formatFileSize(file.size)}</p>
                            )}
                          </div>
                          {!uploadDialog.isLoading && (
                            <button
                              type="button"
                              onClick={() => removeFile(index)}
                              className="flex-shrink-0 p-1 text-muted-foreground hover:text-destructive"
                            >
                              <X className="h-4 w-4" />
                            </button>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>
              )}

              {/* Upload Progress */}
              {uploadProgress && (
                <div className="text-sm text-primary">
                  Uploading file {uploadProgress.current} of {uploadProgress.total}...
                </div>
              )}

              {/* Upload Results */}
              {uploadResults && (
                <div className="border border-border rounded-md divide-y divide-border max-h-32 overflow-y-auto">
                  {uploadResults.map((result, index) => (
                    <div key={index} className="flex items-center px-3 py-1.5 text-sm">
                      <span className={`mr-2 ${result.success ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'}`}>
                        {result.success ? '\u2713' : '\u2717'}
                      </span>
                      <span className="truncate text-foreground">{result.name}</span>
                      {result.error && (
                        <span className="ml-2 text-xs text-destructive truncate">{result.error}</span>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Action Buttons */}
            <div className="flex justify-end gap-2 mt-6">
              <button
                type="button"
                onClick={cancelUpload}
                disabled={uploadDialog.isLoading}
                className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleUploadAll}
                disabled={uploadDialog.isLoading || pendingFiles.length === 0}
                className="px-4 py-2 text-sm bg-primary text-primary-foreground rounded-md hover:bg-primary/90 disabled:opacity-50"
              >
                {uploadDialog.isLoading
                  ? uploadProgress
                    ? `Uploading ${uploadProgress.current}/${uploadProgress.total}...`
                    : 'Uploading...'
                  : `Upload${pendingFiles.length > 0 ? ` (${pendingFiles.length})` : ''}`}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Create Folder Dialog */}
      <CreateFolderDialog
        isOpen={createFolderDialog.isOpen}
        onClose={cancelCreateFolder}
        onCreateFolder={handleCreateFolderSubmit}
        parentPath={createFolderDialog.parentPath}
      />

      {/* Move File/Folder Dialog */}
      <MoveFileDialog
        isOpen={localMoveDialog.isOpen}
        onClose={cancelMove}
        onMove={confirmMove}
        item={localMoveDialog.item}
        destinationPath={localMoveDialog.destinationPath}
        setDestinationPath={(path) => setLocalMoveDialog(prev => ({ ...prev, destinationPath: path }))}
        commitMessage={localMoveDialog.commitMessage}
        setCommitMessage={(message) => setLocalMoveDialog(prev => ({ ...prev, commitMessage: message }))}
        isLoading={localMoveDialog.isLoading}
      />

      {/* Rename File/Folder Dialog */}
      <RenameFileDialog
        isOpen={renameDialog.isOpen}
        onClose={closeRenameDialog}
        onRename={confirmRename}
        item={renameDialog.item}
        isLoading={renameDialog.isLoading}
      />

      </div>
    </TooltipProvider>
  )
}

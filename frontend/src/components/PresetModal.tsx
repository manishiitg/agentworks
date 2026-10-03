import { stripRetiredLLMFallbacks } from '../utils/retiredLLMFallbacks'
import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Button } from './ui/Button';
import { Input } from './ui/Input';
import { Textarea } from './ui/Textarea';
import { Card } from './ui/Card';
import { Folder, Loader2, Plus, Settings, Trash2, X } from 'lucide-react';
import { FolderSelectionDialog } from './FolderSelectionDialog';
import { ToolSelectionSection } from './ToolSelectionSection';

import ConfirmationDialog from './ui/ConfirmationDialog';
import type { CustomPreset } from '../types/preset';
import type { PlannerFile, PresetLLMConfig, AgentLLMConfig } from '../services/api-types';
import { useLLMStore } from '../stores/useLLMStore';
import { useModeStore } from '../stores/useModeStore';

import LLMSelectionDropdown from './LLMSelectionDropdown';
import { RunsOnPicker, type RunsOnSelection } from '../products/work/RunsOnPicker';
import { rememberRunsOn } from '../products/work/runsOnMemory';


import type { LLMOption } from '../types/llm';
import ModalPortal from './ui/ModalPortal';
import { getWorkflowLLMOptions, getWorkflowLLMTierDefaults, getWorkflowProviderOptions } from '../utils/workflowLLMTierDefaults';
import { llmOptionMatchesRef } from '../utils/llmConfigDisplay';
import { mergeCdpPorts } from '../utils/cdpSetup';
import { useChatStore } from '../stores/useChatStore';


interface PresetModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSave: (label: string, query: string, selectedServers?: string[], selectedTools?: string[], selectedSkills?: string[], agentMode?: 'multi-agent' | 'workflow', selectedFolder?: PlannerFile, llmConfig?: PresetLLMConfig, useCodeExecutionMode?: boolean, selectedSecrets?: string[], selectedGlobalSecretNames?: string[] | null, browserMode?: 'none' | 'auto' | 'headless' | 'cdp', cdpPorts?: number[], icon?: string, workflowKind?: 'relay' | 'workflow') => boolean | void | Promise<boolean | void>;
  editingPreset?: CustomPreset | null;
  availableServers?: string[];
  hideAgentModeSelection?: boolean;
  fixedAgentMode?: 'multi-agent' | 'workflow';
  agentMode: string;
  onDeleteWorkflow?: (preset: CustomPreset) => Promise<void>;
  fixedWorkflowKind?: 'relay' | 'workflow';
}

const PresetModal: React.FC<PresetModalProps> = React.memo(({
  isOpen,
  onClose,
  onSave,
  editingPreset,
  availableServers = [],
  hideAgentModeSelection = false,
  fixedAgentMode,
  agentMode: propAgentMode,
  onDeleteWorkflow,
  fixedWorkflowKind,
}) => {
  const [label, setLabel] = useState('');
  const [icon, setIcon] = useState('');
  const [query, setQuery] = useState('');
  const [selectedServers, setSelectedServers] = useState<string[]>([]);
  const [selectedTools, setSelectedTools] = useState<string[]>([]);
  const [selectedSkills, setSelectedSkills] = useState<string[]>([]);
  const [selectedSecrets, setSelectedSecrets] = useState<string[]>([]);
  // Per-preset global secret selection (null = all selected, [] = none, [...] = specific)
  const [selectedGlobalSecrets, setSelectedGlobalSecrets] = useState<string[] | null>([]);
  const [internalAgentMode, setInternalAgentMode] = useState<'multi-agent' | 'workflow'>('multi-agent');
  const [workflowKind, setWorkflowKind] = useState<'relay' | 'workflow'>('workflow');
  const [selectedFolder, setSelectedFolder] = useState<PlannerFile | null>(null);
  const [workflowFolderEdited, setWorkflowFolderEdited] = useState(false);
  const [showFolderDialog, setShowFolderDialog] = useState(false);
  const [folderDialogPosition, setFolderDialogPosition] = useState({ top: 0, left: 0 });
  const [llmConfig, setLlmConfig] = useState<PresetLLMConfig | null>(null);
  // Browser mode and CDP port are edited in the workflow panel now; this
  // dialog only carries the saved values through.
  const [browserMode, setBrowserModeState] = useState<'none' | 'auto' | 'headless' | 'cdp'>('auto');
  const [cdpPort, setCdpPort] = useState(9222);
  const [showDeleteWorkflowConfirm, setShowDeleteWorkflowConfirm] = useState(false);
  const [deletingWorkflow, setDeletingWorkflow] = useState(false);

  const [builderLLM, setBuilderLLM] = useState<AgentLLMConfig | null>(null);
  const [maintenanceLLM, setMaintenanceLLM] = useState<AgentLLMConfig | null>(null);
  const [pulseLLM, setPulseLLM] = useState<AgentLLMConfig | null>(null);
  const [tier1LLM, setTier1LLM] = useState<AgentLLMConfig | null>(null);
  const [tier2LLM, setTier2LLM] = useState<AgentLLMConfig | null>(null);
  const [tier3LLM, setTier3LLM] = useState<AgentLLMConfig | null>(null);
  const [showWorkflowLLMAdvanced, setShowWorkflowLLMAdvanced] = useState(false);
  // Each credential field owns its own entry/saved state; the parent only needs
  // to know whether one still holds unsaved text, so it can block its submit.
  const [unsavedCredentialProvider] = useState<string | null>(null);
  const [isSavingPreset, setIsSavingPreset] = useState(false);

  const { selectedModeCategory, getAgentModeFromCategory } = useModeStore();
  const primaryConfig = useLLMStore(state => state.primaryConfig);
  const availableLLMs = useLLMStore(state => state.availableLLMs);
  const providerManifest = useLLMStore(state => state.providerManifest);
  const providerManifestLoaded = useLLMStore(state => state.providerManifestLoaded);
  const loadProviderManifest = useLLMStore(state => state.loadProviderManifest);
  const getCurrentLLMOption = useLLMStore(state => state.getCurrentLLMOption);
  const loadDefaultsFromBackend = useLLMStore(state => state.loadDefaultsFromBackend);

  const effectiveAgentMode = useMemo(() => {
    if (fixedAgentMode) return fixedAgentMode;
    if (propAgentMode) return propAgentMode as 'multi-agent' | 'workflow';
    return internalAgentMode;
  }, [fixedAgentMode, propAgentMode, internalAgentMode]);

  const workflowLLMOptions = useMemo(
    () => getWorkflowLLMOptions(availableLLMs, providerManifest),
    [availableLLMs, providerManifest]
  );
  const providerProfileOptions = useMemo(
    () => getWorkflowProviderOptions(providerManifest),
    [providerManifest]
  );
  // "Runs on" for a new workflow: the coding CLI and the account (your own signed-in one first),
  // chosen at creation so the first run works without opening the Models panel. Hidden when the
  // deployment locks model settings (the reason the model section was removed on 2026-09-03).
  const llmConfigLocked = useLLMStore(state => state.llmConfigLocked);
  const [runsOn, setRunsOn] = useState<RunsOnSelection | undefined>();
  const runsOnOptions = useMemo(() => providerProfileOptions.map(option => ({
    id: option.provider,
    label: option.label || option.provider,
    provider: option.provider,
    model_id: option.model,
    default: option.provider === primaryConfig.provider,
  })), [providerProfileOptions, primaryConfig.provider]);
  const showRunsOn = effectiveAgentMode === 'workflow' && !editingPreset && !llmConfigLocked && runsOnOptions.length > 0;

  useEffect(() => {
    if (isOpen && effectiveAgentMode === 'workflow' && !providerManifestLoaded) {
      loadProviderManifest();
    }
  }, [effectiveAgentMode, isOpen, loadProviderManifest, providerManifestLoaded]);

  const sanitizeWorkflowFolderName = useCallback((value: string): string => {
    const sanitized = value
      .normalize('NFKD')
      .replace(/[\u0300-\u036f]/g, '')
      .toLowerCase()
      .replace(/[^a-z0-9]/g, '')

    return sanitized || 'workflow'
  }, []);

  const makeWorkflowFolder = useCallback((folderName: string): PlannerFile => ({
    filepath: `Workflow/${folderName}`,
    type: 'folder'
  }), []);

  // CDP connection check

  // Auto-check CDP for both automatic and required-CDP modes.

  const hasLLMOptions = (options?: Record<string, unknown>) => Boolean(options && Object.keys(options).length > 0);
  const toAgentLLMConfig = useCallback((llm: LLMOption): AgentLLMConfig => ({
    ...(llm.id ? { published_llm_id: llm.id } : {}),
    provider: llm.provider as AgentLLMConfig['provider'],
    model_id: llm.model,
    ...(hasLLMOptions(llm.options) ? { options: llm.options } : {}),
  }), []);
  const findLLMOptionForConfig = useCallback((config?: AgentLLMConfig | null): LLMOption | null => {
    if (!config?.provider || !config?.model_id) return null;
    if (config.published_llm_id) {
      const byID = workflowLLMOptions.find(llm => llm.id === config.published_llm_id);
      if (byID) return byID;
    }
    return workflowLLMOptions.find(llm => llmOptionMatchesRef(llm, config)) || null;
  }, [workflowLLMOptions]);

  // Non-workflow presets still use the same explicit role contract.
  const handleLLMSelect = useCallback((llm: LLMOption) => {
    const selected = toAgentLLMConfig(llm);
    setLlmConfig({
      schema_version: 2,
      mode: 'explicit',
      builder_llm: selected,
      maintenance_llm: selected,
      pulse_llm: selected,
      tiered_config: { tier_1: selected, tier_2: selected, tier_3: selected },
    });
  }, [toAgentLLMConfig]);

  // Get current LLM option for display
  const currentLLMOption = useMemo(() => {
    if (llmConfig?.mode === 'provider_profile' && llmConfig.provider) {
      return providerProfileOptions.find(option => option.provider === llmConfig.provider) ?? null;
    }
    if (!llmConfig?.mode && llmConfig?.provider) {
      const legacyProfile = providerProfileOptions.find(option => option.provider === llmConfig.provider);
      if (legacyProfile) return legacyProfile;
    }
    if (llmConfig?.builder_llm) {
      return findLLMOptionForConfig(llmConfig.builder_llm);
    }
    return getCurrentLLMOption();
  }, [llmConfig, findLLMOptionForConfig, getCurrentLLMOption, providerProfileOptions]);

  const defaultAgentLLM = useMemo<AgentLLMConfig | null>(() => {
    if (llmConfig?.builder_llm) {
      return llmConfig.builder_llm;
    }
    if (primaryConfig.provider && primaryConfig.model_id) {
      return {
        provider: primaryConfig.provider as AgentLLMConfig['provider'],
        model_id: primaryConfig.model_id,
        options: primaryConfig.options
      };
    }
    return null;
  }, [llmConfig, primaryConfig]);

  const workflowDefaultTierLLMs = useMemo(() => {
    return currentLLMOption ? getWorkflowLLMTierDefaults(currentLLMOption, providerManifest) : null;
  }, [currentLLMOption, providerManifest]);

  const effectiveTier1LLM = useMemo<AgentLLMConfig | null>(() => tier1LLM || workflowDefaultTierLLMs?.tier1 || defaultAgentLLM, [tier1LLM, workflowDefaultTierLLMs, defaultAgentLLM]);
  const effectiveTier2LLM = useMemo<AgentLLMConfig | null>(() => tier2LLM || workflowDefaultTierLLMs?.tier2 || defaultAgentLLM, [tier2LLM, workflowDefaultTierLLMs, defaultAgentLLM]);
  const effectiveTier3LLM = useMemo<AgentLLMConfig | null>(() => tier3LLM || workflowDefaultTierLLMs?.tier3 || defaultAgentLLM, [tier3LLM, workflowDefaultTierLLMs, defaultAgentLLM]);
  const effectiveBuilderLLM = useMemo<AgentLLMConfig | null>(() => builderLLM || workflowDefaultTierLLMs?.builder || effectiveTier1LLM || defaultAgentLLM, [builderLLM, workflowDefaultTierLLMs, effectiveTier1LLM, defaultAgentLLM]);
  const effectiveMaintenanceLLM = useMemo<AgentLLMConfig | null>(() => maintenanceLLM || workflowDefaultTierLLMs?.maintenance || effectiveTier1LLM || defaultAgentLLM, [maintenanceLLM, workflowDefaultTierLLMs, effectiveTier1LLM, defaultAgentLLM]);
  const effectivePulseLLM = useMemo<AgentLLMConfig | null>(() => pulseLLM || workflowDefaultTierLLMs?.pulse || defaultAgentLLM, [pulseLLM, workflowDefaultTierLLMs, defaultAgentLLM]);


  // Clears any half-typed credential when the modal opens or switches
  // automations, so one workflow's entry can never be submitted against another.

  const hasAdvancedWorkflowLLMConfig = useCallback((presetLLM?: PresetLLMConfig | null) => {
    return presetLLM?.mode === 'explicit';
  }, []);


  useEffect(() => {
    if (editingPreset) {
      console.log('[PresetModal] Loading preset:', editingPreset);
      console.log('[PresetModal] Selected tools from preset:', editingPreset.selectedTools);
      console.log('[PresetModal] Selected skills from preset:', editingPreset.selectedSkills);
      setLabel(editingPreset.label);
      setIcon(editingPreset.icon || '');
      setQuery(editingPreset.query || '');
      setSelectedServers(editingPreset.selectedServers || []);
      setSelectedTools(editingPreset.selectedTools || []); // NEW
      setSelectedSkills(editingPreset.selectedSkills || []);
      setSelectedSecrets(editingPreset.selectedSecrets || []);
      setSelectedGlobalSecrets(editingPreset.selectedGlobalSecretNames ?? null);
      setInternalAgentMode(editingPreset.agentMode || 'workflow'); // Default to workflow
      setWorkflowKind(editingPreset.workflowKind || 'workflow');
      setSelectedFolder(editingPreset.selectedFolder || null);
      setWorkflowFolderEdited(true);
      const presetLLM: PresetLLMConfig = editingPreset.llmConfig || {
        schema_version: 2,
        mode: 'provider_profile',
        provider: primaryConfig.provider as PresetLLMConfig['provider'],
      };
      setLlmConfig(presetLLM);
      // Load browser mode: prefer explicit browserMode, fall back to legacy derivation
      if (editingPreset.browserMode) {
        setBrowserModeState(editingPreset.browserMode);
      } else {
        // Legacy fallback for presets saved before browserMode was added
        if (editingPreset.enableBrowserAccess) {
          setBrowserModeState('headless');
        } else {
          setBrowserModeState('auto');
        }
      }
      setCdpPort(editingPreset.cdpPorts?.[0] || 9222);
      // Load agent-specific configs if available
      setBuilderLLM(presetLLM.builder_llm || null);
      setMaintenanceLLM(presetLLM.maintenance_llm || null);
      setPulseLLM(presetLLM.pulse_llm || null);
      // Load tiered LLM allocation config
      setTier1LLM(presetLLM.tiered_config?.tier_1 || null);
      setTier2LLM(presetLLM.tiered_config?.tier_2 || null);
      setTier3LLM(presetLLM.tiered_config?.tier_3 || null);
      setShowWorkflowLLMAdvanced(hasAdvancedWorkflowLLMConfig(presetLLM));
    } else {
      setLabel('');
      setIcon('');
      setQuery('');
      setSelectedServers([]);
      setSelectedTools([]); // NEW
      setSelectedSkills([]);
      setSelectedSecrets([]);
      setSelectedGlobalSecrets([]);
      // Default to workflow mode as chat presets are disabled
      const defaultMode = 'workflow';
      setInternalAgentMode(defaultMode);
      setWorkflowKind(fixedWorkflowKind || 'workflow');
      setSelectedFolder(makeWorkflowFolder(sanitizeWorkflowFolderName('')));
      setWorkflowFolderEdited(false);
      // Initialize LLM config from current primary config
      const defaultLLM: PresetLLMConfig = {
        schema_version: 2,
        mode: 'provider_profile',
        provider: primaryConfig.provider as PresetLLMConfig['provider'],
      };
      setLlmConfig(defaultLLM);
      setBrowserModeState('auto'); // Prefer connected CDP, otherwise headless
      setCdpPort(9222);
      // Initialize agent-specific configs to null (will use legacy default)
      setBuilderLLM(null);
      setMaintenanceLLM(null);
      setPulseLLM(null);
      // Initialize tiered config
      setTier1LLM(null);
      setTier2LLM(null);
      setTier3LLM(null);
      setShowWorkflowLLMAdvanced(false);
    }
  }, [editingPreset, fixedAgentMode, fixedWorkflowKind, primaryConfig, selectedModeCategory, getAgentModeFromCategory, makeWorkflowFolder, sanitizeWorkflowFolderName, hasAdvancedWorkflowLLMConfig]);

  useEffect(() => {
    if (editingPreset || effectiveAgentMode !== 'workflow' || workflowFolderEdited) {
      return;
    }

    setSelectedFolder(makeWorkflowFolder(sanitizeWorkflowFolderName(label)));
  }, [editingPreset, effectiveAgentMode, label, makeWorkflowFolder, sanitizeWorkflowFolderName, workflowFolderEdited]);

  const handleSelectFolders = useCallback((e: React.MouseEvent) => {
    const rect = e.currentTarget.getBoundingClientRect();
    // Estimate dialog height (max-h-80 = 320px + some padding)
    const estimatedDialogHeight = 320;
    const spaceAbove = rect.top + window.scrollY;
    
    // Always try to position above the button so contents are visible
    // Fallback to below only if there's not enough space above
    const minSpaceNeeded = 200; // Minimum space needed above
    const shouldPositionAbove = spaceAbove >= minSpaceNeeded;
    
    setFolderDialogPosition({
      top: shouldPositionAbove 
        ? rect.top + window.scrollY - estimatedDialogHeight 
        : rect.bottom + window.scrollY,
      left: rect.left + window.scrollX
    });
    setShowFolderDialog(true);
  }, []);

  const handleFolderSelect = useCallback((folder: PlannerFile) => {
    setSelectedFolder(folder);
    setWorkflowFolderEdited(true);
    setShowFolderDialog(false);
  }, []);

  const handleRemoveFolder = useCallback(() => {
    setSelectedFolder(null);
    setWorkflowFolderEdited(true);
  }, []);


  const handleSubmit = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();
    const isQueryRequired = effectiveAgentMode !== 'workflow';
    if (!label.trim() || (isQueryRequired && !query.trim())) return;
    if (effectiveAgentMode === 'workflow' && !selectedFolder) {
      alert('Folder selection is required for workflow presets');
      return;
    }
    // Saving the automation with a pasted credential still sitting unsaved in
    // its box looks like it stored the credential; it does not.
    if (unsavedCredentialProvider) {
      const inputId = unsavedCredentialProvider === 'cursor-cli' ? 'cursor-api-key' : 'claude-code-token';
      useChatStore.getState().addToast('Save or clear the credential before updating the automation.', 'error');
      document.getElementById(inputId)?.focus();
      return;
    }

    setIsSavingPreset(true);
    let manifestSaved = false;
    try {
      
      // Debug: Log what we're sending
      console.log('[PresetModal] Saving preset with:', {
        selectedServers,
        selectedTools,
        selectedSkills,
        label,
        agentMode: effectiveAgentMode
      });
      
      // Build LLM config with workflow-level defaults
      // execution_llm is step-only and is not persisted at the workflow level.
      let finalLLMConfig: PresetLLMConfig | undefined = stripRetiredLLMFallbacks(llmConfig || undefined);
      if (effectiveAgentMode === 'workflow') {
        const workflowBaseLLMConfig = { ...(stripRetiredLLMFallbacks(llmConfig || {}) as PresetLLMConfig & { execution_llm?: unknown; learning_llm?: unknown }) };
        delete workflowBaseLLMConfig.execution_llm;
        delete workflowBaseLLMConfig.learning_llm;
        const explicitTieredConfig = effectiveTier1LLM && effectiveTier2LLM && effectiveTier3LLM ? {
          tier_1: stripRetiredLLMFallbacks(effectiveTier1LLM),
          tier_2: stripRetiredLLMFallbacks(effectiveTier2LLM),
          tier_3: stripRetiredLLMFallbacks(effectiveTier3LLM),
        } : undefined;

        if (showRunsOn && runsOn?.provider) {
          workflowBaseLLMConfig.provider = runsOn.provider as PresetLLMConfig['provider'];
          workflowBaseLLMConfig.connection_id = runsOn.connectionId;
          rememberRunsOn('workflow', runsOn.provider);
        }
        if (!showWorkflowLLMAdvanced) {
          if (!workflowBaseLLMConfig.provider) {
            alert('Select a coding agent provider');
            return;
          }
          finalLLMConfig = {
            ...workflowBaseLLMConfig,
            schema_version: 2,
            mode: 'provider_profile',
            builder_llm: undefined,
            maintenance_llm: undefined,
            pulse_llm: undefined,
            tiered_config: undefined,
          };
        } else {
          if (!effectiveBuilderLLM || !effectiveMaintenanceLLM || !effectivePulseLLM || !explicitTieredConfig) {
            alert('Builder, Maintenance, Pulse, and all three execution tiers are required');
            return;
          }
          finalLLMConfig = {
            ...workflowBaseLLMConfig,
            schema_version: 2,
            mode: 'explicit',
            provider: undefined,
            builder_llm: effectiveBuilderLLM,
            maintenance_llm: effectiveMaintenanceLLM,
            pulse_llm: effectivePulseLLM,
            tiered_config: explicitTieredConfig,
          };
        }
      }
      console.log('[PRESET_MODAL] Agent LLM configs being saved:', {
        builderLLM: builderLLM,
        effectiveBuilderLLM: effectiveBuilderLLM || undefined,
        maintenanceLLM: maintenanceLLM,
        effectiveMaintenanceLLM: effectiveMaintenanceLLM || undefined,
        pulseLLM: pulseLLM,
        effectivePulseLLM: effectivePulseLLM || undefined,
        defaultAgentLLM: defaultAgentLLM || undefined,
        effectiveTier1LLM: effectiveTier1LLM || undefined,
        effectiveTier2LLM: effectiveTier2LLM || undefined,
        effectiveTier3LLM: effectiveTier3LLM || undefined,
        finalLLMConfig: finalLLMConfig,
      });
      const cdpPorts = browserMode === 'auto' || browserMode === 'cdp'
        ? mergeCdpPorts(cdpPort, editingPreset?.cdpPorts)
        : [];
      const saved = await onSave(
        label.trim(),
        effectiveAgentMode === 'workflow' ? '' : query.trim(),
        selectedServers,
        selectedTools,
        selectedSkills, // Skill folder names for workflow
        effectiveAgentMode,
        selectedFolder || undefined,
        stripRetiredLLMFallbacks(finalLLMConfig),
        false, // useCodeExecutionMode — backend determines mode from browser selection
        selectedSecrets, // Secret names for workflow injection
        selectedGlobalSecrets, // Per-preset global secret selection (null=all)
        browserMode, // Browser mode: none|auto|headless|cdp
        cdpPorts,
        icon.trim(),
        workflowKind
      );
      if (saved === false) return;
      manifestSaved = true;
      onClose();
    } catch (error) {
      const serverDetail = (error as { response?: { data?: unknown } })?.response?.data;
      const detail = typeof serverDetail === 'string' && serverDetail.trim() !== ''
        ? serverDetail.trim()
        : error instanceof Error ? error.message : 'Unknown error';
      // Provider credentials are saved by their own field, not here, so a
      // failure after the manifest landed can only be the close itself.
      const message = manifestSaved
        ? `Automation configuration was saved, but the modal could not close: ${detail}`
        : `Failed to save automation: ${detail}`;
      useChatStore.getState().addToast(message, 'error');
    } finally {
      setIsSavingPreset(false);
    }
  }, [label, icon, query, effectiveAgentMode, selectedFolder, selectedServers, selectedTools, selectedSkills, selectedSecrets, selectedGlobalSecrets, llmConfig, builderLLM, effectiveBuilderLLM, maintenanceLLM, effectiveMaintenanceLLM, pulseLLM, effectivePulseLLM, browserMode, cdpPort, editingPreset, onSave, onClose, defaultAgentLLM, effectiveTier1LLM, effectiveTier2LLM, effectiveTier3LLM, showWorkflowLLMAdvanced, unsavedCredentialProvider, showRunsOn, runsOn, workflowKind]);

  // Close modal on escape key
  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };

    if (isOpen) {
      document.addEventListener('keydown', handleEscape);
      return () => document.removeEventListener('keydown', handleEscape);
    }
  }, [isOpen, onClose]);

  // Memoized backdrop click handler
  const handleBackdropClick = useCallback((e: React.MouseEvent) => {
    // Only close if clicking on the backdrop, not on the card
    if (e.target === e.currentTarget) {
      onClose();
    }
  }, [onClose]);

  const handleDeleteWorkflowConfirm = useCallback(async () => {
    if (!editingPreset || !onDeleteWorkflow) return;
    setDeletingWorkflow(true);
    try {
      await onDeleteWorkflow(editingPreset);
      setShowDeleteWorkflowConfirm(false);
    } finally {
      setDeletingWorkflow(false);
    }
  }, [editingPreset, onDeleteWorkflow]);


  if (!isOpen) return null;

  return (
    <ModalPortal>
    <div
      className="fixed inset-0 z-[9999] flex items-center justify-center bg-black/50 backdrop-blur-sm p-2 sm:p-4"
      onClick={handleBackdropClick}
    >
      <Card
        className={`flex w-full flex-col overflow-hidden p-0 max-h-[calc(100dvh-1rem)] sm:max-h-[90vh] ${effectiveAgentMode === 'workflow' ? 'max-w-lg' : 'max-w-6xl'}`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex flex-shrink-0 flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-6 sm:py-4">
          <h2 className="min-w-0 text-lg font-semibold text-foreground sm:text-2xl">
            {effectiveAgentMode === 'workflow'
              ? (editingPreset ? 'Edit Automation' : 'Add Automation')
              : (editingPreset ? 'Edit Preset' : 'Add New Preset')}
          </h2>
          <div className="flex flex-wrap items-center gap-2 sm:justify-end">
            {editingPreset && effectiveAgentMode === 'workflow' && onDeleteWorkflow && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setShowDeleteWorkflowConfirm(true)}
                className="border-red-200 text-red-600 hover:bg-red-50 hover:text-red-700 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950/30"
              >
                <Trash2 className="mr-1 h-4 w-4" />
                Delete Automation
              </Button>
            )}
            <Button
              type="submit"
              form="preset-form"
              variant="outline"
              size="sm"
              disabled={isSavingPreset || !label.trim() || (effectiveAgentMode !== 'workflow' && !query.trim()) || (effectiveAgentMode === 'workflow' && !selectedFolder)}
            >
              {isSavingPreset && <Loader2 className="mr-1 h-4 w-4 animate-spin" />}
              {editingPreset ? 'Update' : 'Save'} {effectiveAgentMode === 'workflow' ? (workflowKind === 'relay' ? 'Relay' : 'Automation') : 'Preset'}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onClose}
              aria-label="Close"
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-6">
        <form id="preset-form" onSubmit={handleSubmit} className="space-y-6">
          {/* Two Column Layout for both modes */}
          {effectiveAgentMode === 'workflow' ? (
            /* Workflow Mode: the name is the whole form. The folder is derived
               from the name (see the effect above), and everything else --
               LLM, servers, skills, secrets, browser -- is configured in the
               workflow's own panel after creation. Existing values are kept
               untouched on save. (LLM section removed 2026-09-03 at the
               user's request: with a deployment-wide LLM lock it only
               confused people.) */
            <div className="mx-auto w-full max-w-lg space-y-4">
              <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-3">
                <div>
                  <label htmlFor="preset-icon" className="mb-2 block text-sm font-medium">
                    Icon
                  </label>
                  <Input
                    id="preset-icon"
                    value={icon}
                    onChange={(e) => setIcon(Array.from(e.target.value).slice(0, 8).join(''))}
                    placeholder="⚙️"
                    aria-describedby="preset-icon-help"
                    className="text-center text-lg"
                  />
                </div>
                <div>
                  <label htmlFor="preset-label" className="block text-sm font-medium mb-2">
                    {workflowKind === 'relay' ? 'Relay Name' : 'Automation Name'}
                  </label>
                  <Input
                    id="preset-label"
                    value={label}
                    onChange={(e) => setLabel(e.target.value)}
                    placeholder="Enter automation name..."
                    autoFocus
                    required
                  />
                </div>
              </div>
              <div>
                <p id="preset-icon-help" className="text-xs text-gray-500 dark:text-gray-400">
                  Optional. Use an emoji or short symbol; otherwise the automation’s initial is used.
                </p>
                {showRunsOn && (
                  <RunsOnPicker profileId="workflow" accountsProduct="" saveAccount="when-needed" options={runsOnOptions} onChange={setRunsOn} />
                )}
                {!editingPreset && (
                  <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
                    Saved under <span className="font-mono">{selectedFolder?.filepath || 'Workflow/workflow'}</span>. Models, secrets and connectors are set up inside the workflow.
                  </p>
                )}
              </div>
              {!editingPreset && !fixedWorkflowKind && <div>
                <label htmlFor="workflow-kind" className="mb-2 block text-sm font-medium">Product</label>
                <select
                  id="workflow-kind"
                  value={workflowKind}
                  onChange={event => setWorkflowKind(event.target.value as 'workflow' | 'relay')}
                  className="w-full rounded-md border border-gray-300 bg-white p-2 text-sm dark:border-gray-600 dark:bg-gray-800"
                >
                  <option value="workflow">Automation</option>
                  <option value="relay">Relay</option>
                </select>
                {workflowKind === 'relay' && <p className="mt-1 text-xs text-gray-500">Build an API callable graph of authored agents, scripts, and decisions.</p>}
              </div>}
            </div>
          ) : (
            /* Simple/Chat Mode: Two Column Layout */
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
              {/* Left Column - Preset Name and Query */}
              <div className="space-y-4">
                <div>
                  <label htmlFor="preset-label" className="block text-sm font-medium mb-2">
                    Preset Name
                  </label>
                  <Input
                    id="preset-label"
                    value={label}
                    onChange={(e) => setLabel(e.target.value)}
                    placeholder="Enter preset name..."
                    required
                  />
                </div>

                <div>
                  <label htmlFor="preset-query" className="block text-sm font-medium mb-2">
                    Query
                  </label>
                  <Textarea
                    id="preset-query"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Enter your query..."
                    rows={24}
                    required
                    className="resize-none"
                  />
                </div>
              </div>

              {/* Right Column - Configuration Options */}
              <div className="space-y-4">
                {/* LLM Configuration */}
                <div>
                  <label className="block text-sm font-medium mb-2 flex items-center gap-2">
                    <Settings className="w-4 h-4" />
                    LLM Configuration
                  </label>
                  <div className="p-3 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-md">
                    <div className="space-y-3">
                      <div>
                        <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-2">
                          Select LLM for this preset
                        </label>
                        <LLMSelectionDropdown
                          availableLLMs={availableLLMs}
                          selectedLLM={currentLLMOption}
                          onLLMSelect={handleLLMSelect}
                          onRefresh={loadDefaultsFromBackend}
                          disabled={false}
                          inModal={true}
                          openDirection="down"
                        />
                      </div>
                      <div className="text-xs text-gray-500">
                        This preset will use the selected LLM configuration
                      </div>
                    </div>
                  </div>
                </div>

                {/* MCP Servers and Tools Selection */}
                {availableServers.length > 0 ? (
                  <ToolSelectionSection
                    availableServers={availableServers}
                    selectedServers={selectedServers}
                    selectedTools={selectedTools}
                    onServerChange={setSelectedServers}
                    onToolChange={setSelectedTools}
                    agentMode={effectiveAgentMode}
                  />
                ) : (
                  <div className="space-y-2">
                    <label className="block text-sm font-medium text-gray-900 dark:text-gray-100">
                      MCP Server Selection
                    </label>
                    <div className="p-3 border border-gray-200 dark:border-gray-700 rounded-md text-xs text-gray-500 dark:text-gray-400">
                      No MCP servers configured. Add servers in the MCP settings sidebar.
                    </div>
                  </div>
                )}

                {/* Folder Selection (Optional for simple mode) */}
                <div>
                  <label className="block text-sm font-medium mb-2">
                    Folder (Optional) - Attach workspace folder to this preset
                  </label>
                  <div className="space-y-2">
                    {selectedFolder && (
                      <div className="flex items-center justify-between p-2 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-md">
                        <div className="flex min-w-0 items-center gap-2">
                          <Folder className="w-4 h-4 text-blue-600" />
                          <span className="truncate text-sm text-gray-900 dark:text-gray-100">{selectedFolder.filepath}</span>
                        </div>
                        <button
                          type="button"
                          onClick={handleRemoveFolder}
                          className="p-1 text-gray-500 hover:text-red-600 transition-colors"
                        >
                          <X className="w-4 h-4" />
                        </button>
                      </div>
                    )}
                    <button
                      type="button"
                      data-folder-button
                      onClick={handleSelectFolders}
                      className="w-full p-3 border-2 border-dashed border-gray-300 dark:border-gray-600 text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-blue-500 rounded-md transition-colors"
                    >
                      <div className="flex items-center justify-center gap-2">
                        <Plus className="w-4 h-4" />
                        <span>{selectedFolder ? 'Change Folder' : 'Select Folder'}</span>
                      </div>
                    </button>
                  </div>
                  {selectedFolder && (
                    <p className="text-xs text-gray-500 mt-1">
                      Selected: {selectedFolder.filepath}
                    </p>
                  )}
                </div>

                {/* Agent Mode Selection */}
                {!hideAgentModeSelection && (
                  <div>
                    <label className="block text-sm font-medium mb-2">
                      Agent Mode
                    </label>
                    <div className="grid grid-cols-2 gap-2">
                      {[
                        { value: 'simple', label: 'Simple', description: 'Ask simple questions' },
                        { value: 'workflow', label: 'Automation', description: 'Todo-list execution' }
                      ].map((mode) => (
                        <div key={mode.value} className="flex items-center space-x-2">
                          <input
                            type="radio"
                            id={`agent-mode-${mode.value}`}
                            name="agentMode"
                            value={mode.value}
                            checked={internalAgentMode === mode.value}
                            onChange={(e) => setInternalAgentMode(e.target.value as 'multi-agent' | 'workflow')}
                            className="w-4 h-4 text-blue-600 bg-gray-100 border-gray-300 focus:ring-blue-500"
                          />
                          <label
                            htmlFor={`agent-mode-${mode.value}`}
                            className="text-sm cursor-pointer flex-1"
                          >
                            <div className="font-medium">{mode.label}</div>
                            <div className="text-xs text-gray-500">{mode.description}</div>
                          </label>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {hideAgentModeSelection && fixedAgentMode && (
                  <div>
                    <label className="block text-sm font-medium mb-2">
                      Agent Mode
                    </label>
                    <div className="p-3 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-md">
                      <div className="flex items-center gap-2">
                        <div className="font-medium text-gray-900 dark:text-white">Simple</div>
                        <div className="text-xs text-gray-500 dark:text-gray-400">Ask simple questions</div>
                      </div>
                    </div>
                  </div>
                )}

              </div>
            </div>
          )}
        </form>

        {/* Folder Selection Dialog */}
        <FolderSelectionDialog
          isOpen={showFolderDialog}
          onClose={() => setShowFolderDialog(false)}
          onSelectFolder={handleFolderSelect}
          searchQuery=""
          position={folderDialogPosition}
          agentMode={effectiveAgentMode as 'multi-agent' | 'workflow'}
        />
        <ConfirmationDialog
          isOpen={showDeleteWorkflowConfirm}
          onClose={() => !deletingWorkflow && setShowDeleteWorkflowConfirm(false)}
          onConfirm={handleDeleteWorkflowConfirm}
          title="Delete Automation"
          message={
            editingPreset?.selectedFolder?.filepath
              ? `Delete automation "${editingPreset.label}" and permanently remove the folder \`${editingPreset.selectedFolder.filepath}\`? This cannot be undone.`
              : `Delete automation "${editingPreset?.label || ''}"? This cannot be undone.`
          }
          confirmText="Delete Automation"
          type="danger"
          isLoading={deletingWorkflow}
        />
        </div>
      </Card>
    </div>
    </ModalPortal>
  );
});

PresetModal.displayName = 'PresetModal';

export default PresetModal;

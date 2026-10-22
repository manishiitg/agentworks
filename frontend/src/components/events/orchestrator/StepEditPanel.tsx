import React, { useState, useEffect, useRef } from 'react';
import { ChevronDown, ChevronUp, Settings, Sparkles, Code2 } from 'lucide-react';
import { Button } from '../../ui/Button';
import ProviderAccounts from '../../providers/ProviderAccounts';
import LLMSelectionDropdown from '../../LLMSelectionDropdown';
import { ToolSelectionSection } from '../../ToolSelectionSection';
import { usePresetApplication } from '../../../stores/useGlobalPresetStore';
import type { TodoStepWithConfigs, AgentConfigs, AgentLLMConfig } from '../../../utils/stepConfigMatching';
import type { PresetLLMConfig } from '../../../services/api-types';
import type { LLMOption } from '../../../types/llm';
import { useLLMStore } from '../../../stores/useLLMStore';
import { useCapabilitiesStore } from '../../../stores/useCapabilitiesStore';
import { Tooltip, TooltipContent, TooltipTrigger, TooltipProvider } from '../../ui/tooltip';
import {
  HUMAN_TOOLS,
  getToolsByCategory,
  getCategoryForTool,
} from '../../../utils/customToolNames';
interface StepEditPanelProps {
  step: TodoStepWithConfigs;
  stepIndex: number;
  onSave: (updatedStep: TodoStepWithConfigs) => Promise<void>;
  onCancel: () => void;
  isSaving?: boolean;
  presetServers?: string[]; // Preset's selected servers (subset to show in UI)
  presetLLMConfig?: PresetLLMConfig | null; // Preset's LLM config with agent defaults
  presetUseCodeExecutionMode?: boolean; // Preset's code execution mode (default value for step)
  isExpanded?: boolean; // Controlled expanded state from parent
  onToggleExpanded?: (expanded: boolean) => void; // Callback when expansion state changes
}

export const StepEditPanel: React.FC<StepEditPanelProps> = ({
  step,
  stepIndex,
  onSave,
  isSaving = false,
  presetServers = [],
  presetLLMConfig = null,
  presetUseCodeExecutionMode = false,
  isExpanded: controlledIsExpanded,
  onToggleExpanded,
}) => {
  const { availableLLMs, getCurrentLLMOption, loadDefaultsFromBackend } = useLLMStore();
  const { currentPresetTools } = usePresetApplication();
  const { capabilities } = useCapabilitiesStore();
  
  // Use controlled state if provided, otherwise fall back to local state
  const [localIsExpanded, setLocalIsExpanded] = useState(false);
  const isExpanded = controlledIsExpanded !== undefined ? controlledIsExpanded : localIsExpanded;
  
  const handleToggleExpanded = () => {
    const newExpanded = !isExpanded;
    if (onToggleExpanded) {
      onToggleExpanded(newExpanded);
    } else {
      setLocalIsExpanded(newExpanded);
    }
  };

  // Initialize state from step's agent_configs
  // Ensure validation is enabled for loop steps (required to check loop conditions)
  // Ensure validation and learning are enabled for code execution mode
  const [agentConfigs, setAgentConfigs] = useState<AgentConfigs>(() => {
    const configs = step.agent_configs || {};
    console.log('[StepConfigDebug] Initializing agentConfigs from step:', {
      stepTitle: step.title,
      stepId: step.id,
      step_agent_configs: step.agent_configs,
      configs,
    });
    
    return configs;
  });

  // Initialize step-level server/tool selection
  // If step config has selection, use it; otherwise use preset defaults for display
  const [selectedServers, setSelectedServers] = useState<string[]>(() => {
    // If step config has explicit selection, use it
    if (agentConfigs.selected_servers && agentConfigs.selected_servers.length > 0) {
      return agentConfigs.selected_servers;
    }
    // Otherwise, use preset defaults for display
    return presetServers;
  });
  const [selectedTools, setSelectedTools] = useState<string[]>(() => {
    // If step config has explicit selection, use it
    if (agentConfigs.selected_tools && agentConfigs.selected_tools.length > 0) {
      console.log('[StepConfigDebug] ⚠️ CRITICAL: Initializing selectedTools from agentConfigs:', {
        stepId: step.id,
        agentConfigs_selected_tools: agentConfigs.selected_tools,
        using: agentConfigs.selected_tools
      });
      return agentConfigs.selected_tools;
    }
    // Otherwise, use preset defaults for display
    console.log('[StepConfigDebug] ⚠️ CRITICAL: Initializing selectedTools from preset (no step config):', {
      stepId: step.id,
      step_agent_configs: step.agent_configs,
      agentConfigs_selected_tools: agentConfigs.selected_tools,
      currentPresetTools,
      using: currentPresetTools || []
    });
    return currentPresetTools || [];
  });

  // Check if NO_SERVERS is explicitly selected
  const hasNoServers = selectedServers.includes("NO_SERVERS");
  const actualSelectedServers = selectedServers.filter(s => s !== "NO_SERVERS");

  // Helper functions for format conversion (must be defined before useState that uses them)
  const formatToolEntry = (category: string, tool: string): string => {
    return `${category}:${tool}`;
  };

  // State for enabled custom tools in unified format: "category:tool" or "category:*"
  // Default: advanced + human tools only (matches backend default)
  const [enabledCustomTools, setEnabledCustomTools] = useState<string[]>(() => {
    const configs = step.agent_configs || {};
    if (configs.enabled_custom_tools && configs.enabled_custom_tools.length > 0) {
      return configs.enabled_custom_tools;
    }
    // Default: advanced + human tools only (matches backend prepareCustomTools default)
    return ['workspace_advanced:*', 'human_tools:*'];
  });

  // State for expanded tool categories (to show individual tools)
  // workspace_tools expanded by default to show tool configuration
  const [expandedToolCategories, setExpandedToolCategories] = useState<Set<string>>(new Set(['workspace_tools']));
  // State for expanded workspace sub-categories (expanded by default to show tools)
  // Maps to backend categories: workspace_advanced, workspace_image, workspace_browser
  // Note: expandedWorkspaceSubCategories removed - workspace tools are shown flat now

  // Track the step to detect when step changes (using title + index as stable identifier)
  // This ensures state resets properly when switching between different steps
  const stepIdentifier = `${step.title || ''}-${stepIndex}`;
  const prevStepIdentifierRef = useRef<string>(stepIdentifier);
  const prevAgentConfigsRef = useRef<string>(''); // Track previous agent_configs as JSON string

  // Sync state when step changes (different step identifier) OR when agent_configs changes (after save)
  // This ensures state updates both when switching steps and when the same step's config is updated
  useEffect(() => {
    const isDifferentStep = prevStepIdentifierRef.current !== stepIdentifier;
    const currentConfigs = step.agent_configs || {};
    const currentConfigsJson = JSON.stringify(currentConfigs);
    const configsChanged = prevAgentConfigsRef.current !== currentConfigsJson;
    
    // Sync if it's a different step OR if agent_configs has changed (e.g., after save)
    // We need to sync when agent_configs changes to reflect saved changes
    if (isDifferentStep || configsChanged) {
      // Critical log - always show this to track the issue
      console.log('[StepConfigDebug] ⚠️ CRITICAL: Syncing state from step config:', {
        stepId: step.id,
        stepTitle: step.title,
        currentConfigs_selected_tools: currentConfigs.selected_tools,
        currentConfigs_selected_servers: currentConfigs.selected_servers,
        step_agent_configs_selected_tools: step.agent_configs?.selected_tools,
        step_agent_configs_selected_servers: step.agent_configs?.selected_servers,
        fullStepAgentConfigs: step.agent_configs,
      });
      
      // Reset agentConfigs state from step's config
      // Force enable validation for loop steps
      setAgentConfigs({ ...currentConfigs });
      
      // Update servers: use step config if available, otherwise preset defaults
      if (currentConfigs.selected_servers && currentConfigs.selected_servers.length > 0) {
        // Check if NO_SERVERS is in the config
        setSelectedServers(currentConfigs.selected_servers);
      } else {
        setSelectedServers(presetServers);
      }

      // Update tools: use step config if available, otherwise preset defaults
      if (currentConfigs.selected_tools && currentConfigs.selected_tools.length > 0) {
        console.log('[StepConfigDebug] ⚠️ CRITICAL: Setting selectedTools from step config:', {
          stepId: step.id,
          from: currentConfigs.selected_tools,
          settingTo: currentConfigs.selected_tools
        });
        setSelectedTools(currentConfigs.selected_tools);
      } else {
        console.log('[StepConfigDebug] ⚠️ CRITICAL: No step config tools, using preset:', {
          stepId: step.id,
          currentPresetTools,
          settingTo: currentPresetTools || []
        });
        setSelectedTools(currentPresetTools || []);
      }

      // Update enabled custom tools: convert from old format if needed
      if (currentConfigs.enabled_custom_tools && currentConfigs.enabled_custom_tools.length > 0) {
        setEnabledCustomTools(currentConfigs.enabled_custom_tools);
      } else {
        // No tools specified - default to advanced + human tools only (matches backend default)
        setEnabledCustomTools(['workspace_advanced:*', 'human_tools:*']);
      }

      // Reset expanded categories when step changes (keep workspace_tools expanded by default)
      setExpandedToolCategories(new Set(['workspace_tools']));

      // Update refs for next comparison
      prevStepIdentifierRef.current = stepIdentifier;
      prevAgentConfigsRef.current = currentConfigsJson;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stepIdentifier, step.agent_configs, presetServers, currentPresetTools]); // Include stepIdentifier and agent_configs to detect changes

  // Helper to convert AgentLLMConfig to LLMOption
  const llmConfigToOption = (config: AgentLLMConfig | undefined): LLMOption | null => {
    if (!config || !config.provider || !config.model_id) {
      return null;
    }
    if (config.published_llm_id) {
      const byID = availableLLMs.find((l) => l.id === config.published_llm_id);
      if (byID) return byID;
    }
    const llm = availableLLMs.find(
      (l) => l.provider === config.provider && l.model === config.model_id
    );
    return llm || null;
  };

  // Helper to get preset default LLM for an agent type
  const getPresetDefaultLLM = (agentType: 'execution'): LLMOption | null => {
    if (!presetLLMConfig) {
      return null;
    }
    let config: AgentLLMConfig | undefined;
    if (agentType === 'execution') {
      config = presetLLMConfig.tiered_config?.tier_1 || presetLLMConfig.builder_llm;
    }
    if (config) {
      return llmConfigToOption(config);
    }
    return null;
  };

  // Helper to convert LLMOption to AgentLLMConfig
  const optionToLLMConfig = (option: LLMOption | null): AgentLLMConfig | undefined => {
    if (!option) {
      return undefined;
    }
    return {
      ...(option.id ? { published_llm_id: option.id } : {}),
      provider: option.provider as AgentLLMConfig['provider'],
      model_id: option.model,
      ...(option.options && Object.keys(option.options).length > 0 ? { options: option.options } : {}),
    };
  };

  // Helper functions for unified format: "category:tool" or "category:*"
  const parseToolEntry = (entry: string): { category: string; tool: string } | null => {
    // Split only on first colon to handle tool names that might contain colons
    const colonIndex = entry.indexOf(':');
    if (colonIndex === -1 || colonIndex === 0) return null;
    return { 
      category: entry.substring(0, colonIndex), 
      tool: entry.substring(colonIndex + 1) 
    };
  };

  // Sub-categories that belong to workspace_tools parent
  const WORKSPACE_SUB_CATEGORIES = ['workspace_advanced', 'workspace_image', 'workspace_browser'];

  const isCategoryEnabled = (category: string, enabledTools: string[]): boolean => {
    // Empty array means all tools enabled by default
    if (enabledTools.length === 0) return true;
    return enabledTools.includes(formatToolEntry(category, '*'));
  };

  const isToolEnabled = (category: string, toolName: string, enabledTools: string[]): boolean => {
    // Empty array means all tools enabled by default
    if (enabledTools.length === 0) return true;
    // Check if parent workspace_tools:* is enabled (covers all sub-categories)
    if (WORKSPACE_SUB_CATEGORIES.includes(category) && isCategoryEnabled('workspace_tools', enabledTools)) return true;
    // Check if sub-category is enabled (e.g., workspace_advanced:*)
    if (isCategoryEnabled(category, enabledTools)) return true;
    // Check if specific tool is enabled (e.g., workspace_advanced:read_image)
    return enabledTools.includes(formatToolEntry(category, toolName));
  };

  // Sentinel value: represents "no tools enabled". Backend won't match this category,
  // so no tools pass the filter. Distinct from [] which means "all enabled by default".
  const NONE_ENABLED_SENTINEL = 'none:*';

  const enableCategory = (category: string, enabledTools: string[]): string[] => {
    // Remove any specific tools from this category + sentinel, add category:*
    const filtered = enabledTools.filter(entry => {
      if (entry === NONE_ENABLED_SENTINEL) return false;
      const parsed = parseToolEntry(entry);
      return !parsed || parsed.category !== category;
    });
    return [...filtered, formatToolEntry(category, '*')];
  };

  const disableCategory = (category: string, enabledTools: string[]): string[] => {
    // If array is empty (default = all enabled), explicitly enable all other categories
    if (enabledTools.length === 0) {
      if (category === 'workspace_tools') {
        return [formatToolEntry('human_tools', '*')];
      } else if (category === 'human_tools') {
        return [formatToolEntry('workspace_tools', '*')];
      } else if (WORKSPACE_SUB_CATEGORIES.includes(category)) {
        const allWsTools = getToolsByCategory('workspace_tools', capabilities?.workspace);
        const disabledTools = new Set(getToolsByCategory(category, capabilities?.workspace));
        const result: string[] = [];
        for (const tool of allWsTools) {
          if (!disabledTools.has(tool)) {
            const cat = getCategoryForTool(tool) || 'workspace_tools';
            result.push(formatToolEntry(cat, tool));
          }
        }
        result.push(formatToolEntry('human_tools', '*'));
        return result;
      }
    }

    // For workspace_tools, also remove all sub-category entries
    if (category === 'workspace_tools') {
      const result = enabledTools.filter(entry => {
        if (entry === NONE_ENABLED_SENTINEL) return false;
        const parsed = parseToolEntry(entry);
        if (!parsed) return true;
        return !parsed.category.startsWith('workspace');
      });
      // Use sentinel if result would be empty — [] means "all enabled by default"
      return result.length > 0 ? result : [NONE_ENABLED_SENTINEL];
    }

    // Remove category:* and all specific tools from this category
    const result = enabledTools.filter(entry => {
      if (entry === NONE_ENABLED_SENTINEL) return false;
      const parsed = parseToolEntry(entry);
      return !parsed || parsed.category !== category;
    });
    // Use sentinel if result would be empty — [] means "all enabled by default"
    return result.length > 0 ? result : [NONE_ENABLED_SENTINEL];
  };

  const enableTool = (category: string, toolName: string, enabledTools: string[]): string[] => {
    let filtered = enabledTools;

    // If parent workspace_tools:* is enabled, expand it to sub-categories first
    if (WORKSPACE_SUB_CATEGORIES.includes(category) && isCategoryEnabled('workspace_tools', enabledTools)) {
      filtered = filtered.filter(e => e !== formatToolEntry('workspace_tools', '*'));
      const allWsTools = getToolsByCategory('workspace_tools', capabilities?.workspace);
      for (const tool of allWsTools) {
        const cat = getCategoryForTool(tool) || 'workspace_tools';
        filtered.push(formatToolEntry(cat, tool));
      }
    }

    // If sub-category is enabled, disable it first (switch to specific tools)
    if (isCategoryEnabled(category, filtered)) {
      filtered = filtered.filter(e => e !== formatToolEntry(category, '*'));
      const allCategoryTools = getToolsByCategory(category, capabilities?.workspace);
      filtered = [...filtered, ...allCategoryTools.map(t => formatToolEntry(category, t))];
    }

    // Add this specific tool if not already present
    const toolEntry = formatToolEntry(category, toolName);
    if (!filtered.includes(toolEntry)) {
      filtered = [...filtered, toolEntry];
    }
    return filtered;
  };

  const disableTool = (category: string, toolName: string, enabledTools: string[]): string[] => {
    let filtered = enabledTools;

    // If parent workspace_tools:* is enabled, expand it to sub-categories first
    if (WORKSPACE_SUB_CATEGORIES.includes(category) && isCategoryEnabled('workspace_tools', enabledTools)) {
      filtered = filtered.filter(e => e !== formatToolEntry('workspace_tools', '*'));
      const allWsTools = getToolsByCategory('workspace_tools', capabilities?.workspace);
      for (const tool of allWsTools) {
        const cat = getCategoryForTool(tool) || 'workspace_tools';
        filtered.push(formatToolEntry(cat, tool));
      }
    }

    // If sub-category is enabled, disable it and enable all other tools in that sub-category
    if (isCategoryEnabled(category, filtered)) {
      filtered = filtered.filter(e => e !== formatToolEntry(category, '*'));
      const allCategoryTools = getToolsByCategory(category, capabilities?.workspace);
      const otherTools = allCategoryTools.filter(t => t !== toolName);
      filtered = [...filtered, ...otherTools.map(t => formatToolEntry(category, t))];
      return filtered;
    }

    // Just remove this specific tool
    return filtered.filter(entry => entry !== formatToolEntry(category, toolName));
  };

  // Update execution LLM
  const handleExecutionLLMSelect = (llm: LLMOption) => {
    setAgentConfigs((prev) => ({
      ...prev,
      execution_llm: {...optionToLLMConfig(llm), connection_id: prev.execution_llm?.provider === llm.provider ? prev.execution_llm.connection_id : (presetLLMConfig?.provider === llm.provider ? presetLLMConfig.connection_id : presetLLMConfig?.tiered_config?.tier_1?.provider===llm.provider ? presetLLMConfig.tiered_config.tier_1.connection_id : undefined)},
    }));
  };

  // Update toggles
  const handleToggleChange = (key: keyof AgentConfigs, value: boolean) => {
    setAgentConfigs((prev) => ({
      ...prev,
      [key]: value,
    }));
  };

  // Handle save
  const handleSave = async () => {
    // Start with a clean config object - we'll explicitly set each field
    const finalConfigs: AgentConfigs = {
      ...agentConfigs,
    };
    
    // Handle server/tool selection:
    // - If NO_SERVERS is selected → save ["NO_SERVERS"] explicitly
    // - If user has selected servers/tools → save them
    // - If user has deselected all servers/tools → set to undefined (will use preset defaults)
    if (hasNoServers) {
      // Explicitly save NO_SERVERS to indicate no servers should be used
      finalConfigs.selected_servers = ["NO_SERVERS"];
      finalConfigs.selected_tools = []; // No tools when NO_SERVERS is selected
    } else if (selectedServers.length > 0) {
      finalConfigs.selected_servers = selectedServers;
    } else {
      // Explicitly set to undefined to remove any existing saved selection
      // This ensures the step falls back to preset defaults
      finalConfigs.selected_servers = undefined;
    }

    if (hasNoServers) {
      // No tools when NO_SERVERS is selected
      finalConfigs.selected_tools = [];
    } else if (selectedTools.length > 0) {
      finalConfigs.selected_tools = selectedTools;
    } else {
      // Explicitly set to undefined to remove any existing saved selection
      // This ensures the step falls back to preset defaults
      finalConfigs.selected_tools = undefined;
    }

    // Handle custom tools in unified format: "category:tool" or "category:*"
    if (enabledCustomTools.length === 0) {
      // Empty array means all tools enabled (default behavior)
      finalConfigs.enabled_custom_tools = undefined;
    } else {
      finalConfigs.enabled_custom_tools = enabledCustomTools;
    }

    // Handle use_code_execution_mode: save if explicitly set by user
    // If undefined, step will use preset default (field will be omitted from JSON)
    if (agentConfigs.use_code_execution_mode !== undefined) {
      // User has explicitly set it - save the value
      finalConfigs.use_code_execution_mode = agentConfigs.use_code_execution_mode;
    } else {
      // Not explicitly set - delete the field so it uses preset default
      // JSON.stringify will omit undefined fields automatically
      delete finalConfigs.use_code_execution_mode;
    }

    // Debug logging to verify data being saved
    console.log('[StepEditPanel] Saving step config:', {
      stepTitle: step.title,
      selectedServers,
      selectedTools,
      agentConfigs_use_code_execution_mode: agentConfigs.use_code_execution_mode,
      finalConfigs_use_code_execution_mode: finalConfigs.use_code_execution_mode,
      finalConfigs: {
        selected_servers: finalConfigs.selected_servers,
        selected_tools: finalConfigs.selected_tools,
        use_code_execution_mode: finalConfigs.use_code_execution_mode,
      },
    });

    const updatedStep: TodoStepWithConfigs = {
      ...step,
      agent_configs: finalConfigs,
    };
    await onSave(updatedStep);
    
    // Collapse the panel after saving
    if (onToggleExpanded) {
      onToggleExpanded(false);
    } else {
      setLocalIsExpanded(false);
    }
  };

  // Get current agent config summary (LLM settings only)
  const getAgentConfigSummary = () => {
    // Priority: step config > preset default > global default
    const execLLM = llmConfigToOption(agentConfigs.execution_llm) || getPresetDefaultLLM('execution') || getCurrentLLMOption();
    
    // Get effective code execution mode (step config > preset default)
    const effectiveCodeExecMode = agentConfigs.use_code_execution_mode !== undefined 
      ? agentConfigs.use_code_execution_mode 
      : presetUseCodeExecutionMode;
    
    const parts = [];
    if (execLLM) {
      let codeExecLabel = 'Simple';
      if (effectiveCodeExecMode) codeExecLabel = 'Code Exec';
      parts.push(`Exec: ${execLLM.label} (${codeExecLabel})`);
    }
    
    return parts.length > 0 ? parts.join(' • ') : 'Default config';
  };

  // Get MCP config summary with detailed information
  const getMCPConfigSummary = () => {
    if (hasNoServers) {
      return 'No servers (Pure LLM mode)';
    }
    
    if (selectedServers.length === 0) {
      // Using preset defaults
      return `Using preset defaults (${presetServers.length} servers)`;
    }
    
    const parts = [];
    
    // Server details
    if (selectedServers.length === 1) {
      parts.push(`Server: ${selectedServers[0]}`);
    } else {
      parts.push(`Servers: ${selectedServers.length} (${selectedServers.slice(0, 2).join(', ')}${selectedServers.length > 2 ? '...' : ''})`);
    }
    
    // Tool details
    if (selectedTools.length > 0) {
      const allToolsServers = selectedTools.filter(t => t.endsWith(':*')).map(t => t.replace(':*', ''));
      const specificTools = selectedTools.filter(t => !t.endsWith(':*'));
      
      if (allToolsServers.length > 0) {
        if (allToolsServers.length === 1) {
          parts.push(`All tools from ${allToolsServers[0]}`);
        } else {
          parts.push(`All tools from ${allToolsServers.length} servers`);
        }
      }
      
      if (specificTools.length > 0) {
        if (specificTools.length === 1) {
          parts.push(`1 specific tool`);
        } else {
          parts.push(`${specificTools.length} specific tools`);
        }
      }
    } else {
      parts.push('No tools selected');
    }
    
    return parts.join(' • ');
  };

  // Get custom tools summary with detailed information (using unified format)
  const getCustomToolsSummary = () => {
    const allWorkspaceTools = getToolsByCategory('workspace_tools', capabilities?.workspace);
    const allHumanTools = getToolsByCategory('human_tools', capabilities?.workspace);
    
    // Check if no filtering (default: all enabled)
    if (enabledCustomTools.length === 0) {
      const defaultParts = ['All custom tools enabled (default)'];
      return defaultParts.join(' • ');
    }
    
    const parts = [];
    
    // Parse enabled tools
    const workspaceCategoryEnabled = isCategoryEnabled('workspace_tools', enabledCustomTools);
    const humanCategoryEnabled = isCategoryEnabled('human_tools', enabledCustomTools);
    
    // Get specific tools enabled
    const workspaceSpecificTools: string[] = [];
    const humanSpecificTools: string[] = [];
    
    for (const entry of enabledCustomTools) {
      const parsed = parseToolEntry(entry);
      if (!parsed) continue;
      
      if (parsed.category === 'workspace_tools' && parsed.tool !== '*') {
        workspaceSpecificTools.push(parsed.tool);
      } else if (parsed.category === 'human_tools' && parsed.tool !== '*') {
        humanSpecificTools.push(parsed.tool);
      }
    }
    
    // Workspace tools summary
    if (workspaceCategoryEnabled) {
      parts.push('All workspace tools');
    } else if (workspaceSpecificTools.length > 0) {
      if (workspaceSpecificTools.length === allWorkspaceTools.length) {
        parts.push('All workspace tools');
      } else {
        // Show sub-category breakdown (matches backend categories)
        const advancedTools = getToolsByCategory('workspace_advanced', capabilities?.workspace);
        const advancedEnabled = workspaceSpecificTools.filter(t => advancedTools.includes(t)).length;

        if (advancedEnabled > 0) {
          parts.push(`${workspaceSpecificTools.length}/${allWorkspaceTools.length} workspace (${advancedEnabled}/${advancedTools.length} advanced)`);
        } else {
          parts.push(`${workspaceSpecificTools.length}/${allWorkspaceTools.length} workspace tools`);
        }
      }
    }
    
    // Human tools summary
    if (humanCategoryEnabled) {
      parts.push('All human tools');
    } else if (humanSpecificTools.length > 0) {
      if (humanSpecificTools.length === allHumanTools.length) {
        parts.push('All human tools');
      } else {
        parts.push(`${humanSpecificTools.length}/${allHumanTools.length} human tools`);
      }
    } else if (!workspaceCategoryEnabled && workspaceSpecificTools.length > 0) {
      // Workspace tools enabled but human tools disabled
      parts.push('0/1 human tools');
    }
    
    return parts.length > 0 ? parts.join(' • ') : 'No custom tools';
  };

  return (
    <div className="mt-2 border-t border-gray-200 dark:border-gray-700 pt-2">
      {step.has_human_input && <p className="text-sm text-muted-foreground">Historical human-input step. This step type is retired; migrate the saved workflow before running it.</p>}

      {/* Agent configuration is not editable for retired historical step events */}
      {!step.has_human_input && (
        <>
      {/* Compact Header - Always Visible */}
      <div 
        className="cursor-pointer hover:bg-gray-100 dark:hover:bg-gray-700/30 rounded px-2 py-1.5 -mx-2 transition-colors"
        onClick={handleToggleExpanded}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 flex-1 min-w-0">
            <Settings className="w-3.5 h-3.5 text-gray-500 dark:text-gray-400 flex-shrink-0" />
            <span className="text-xs font-medium text-gray-600 dark:text-gray-400">
              Agent Config
            </span>
            <span className="text-xs text-gray-500 dark:text-gray-500 truncate">
              {getAgentConfigSummary()}
            </span>
          </div>
          <div className="flex items-center gap-2 flex-shrink-0">
            {isExpanded ? (
              <ChevronUp className="w-3.5 h-3.5 text-gray-500 dark:text-gray-400" />
            ) : (
              <ChevronDown className="w-3.5 h-3.5 text-gray-500 dark:text-gray-400" />
            )}
          </div>
        </div>
        {/* MCP Config on separate line */}
        <div className="flex items-center gap-2 mt-1 ml-6">
          <span className="text-xs font-medium text-gray-600 dark:text-gray-400">
            MCP Config:
          </span>
          <span className="text-xs text-gray-500 dark:text-gray-500">
            {getMCPConfigSummary()}
          </span>
        </div>
        {/* Custom Tools Config on separate line */}
        <div className="flex items-center gap-2 mt-1 ml-6">
          <span className="text-xs font-medium text-gray-600 dark:text-gray-400">
            Custom Tools:
          </span>
          <span className="text-xs text-gray-500 dark:text-gray-500">
            {getCustomToolsSummary()}
          </span>
        </div>
      </div>

      {/* Expanded Configuration Panel */}
      {isExpanded && (
        <div className="mt-2 p-3 bg-gray-50 dark:bg-gray-900/20 border border-gray-200 dark:border-gray-700 rounded-lg">
          <div className="space-y-4">

            {/* Execution Agent Configuration */}
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <div className="text-xs font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">
                  Execution
                </div>
                {/* Code Execution Mode Toggle */}
                <div className="flex items-center border border-gray-300 dark:border-gray-600 rounded-md overflow-hidden">
                  <TooltipProvider>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <button
                          type="button"
                          onClick={() => {
                            console.log('[StepEditPanel] Setting mode to Simple');
                            setAgentConfigs((prev) => ({
                              ...prev,
                              use_code_execution_mode: false,
                            }));
                          }}
                          className={`px-2 py-1 text-xs font-medium transition-colors border-r border-gray-300 dark:border-gray-600 ${
                            (agentConfigs.use_code_execution_mode === false ||
                            (agentConfigs.use_code_execution_mode === undefined && !presetUseCodeExecutionMode))
                              ? 'agent-mode-selected rounded-l-md rounded-r-none'
                              : 'agent-mode-unselected rounded-none'
                          }`}
                        >
                          <Sparkles className="w-3 h-3 inline mr-1" />
                          Simple
                        </button>
                      </TooltipTrigger>
                      <TooltipContent>
                        <p>Simple mode - Direct MCP tool access</p>
                      </TooltipContent>
                    </Tooltip>
                    
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <button
                          type="button"
                          onClick={() => {
                            console.log('[StepEditPanel] Setting mode to Code Exec');
                            setAgentConfigs((prev) => ({
                              ...prev,
                              use_code_execution_mode: true,
                            }));
                          }}
                          className={`px-2 py-1 text-xs font-medium transition-colors ${
                            (agentConfigs.use_code_execution_mode === true ||
                            (agentConfigs.use_code_execution_mode === undefined && presetUseCodeExecutionMode))
                              ? 'agent-mode-selected rounded-r-md rounded-l-none'
                              : 'agent-mode-unselected rounded-none'
                          }`}
                        >
                          <Code2 className="w-3 h-3 inline mr-1" />
                          Code Exec
                        </button>
                      </TooltipTrigger>
                      <TooltipContent>
                        <p>Code Exec mode - MCP tools accessed via generated Go code</p>
                      </TooltipContent>
                    </Tooltip>

                  </TooltipProvider>
                </div>
              </div>
	              <div className="flex items-center gap-2">
	                <div className="flex-1 min-w-0">
	                  <LLMSelectionDropdown
	                    availableLLMs={availableLLMs}
	                    selectedLLM={llmConfigToOption(agentConfigs.execution_llm) || getPresetDefaultLLM('execution') || getCurrentLLMOption()}
                    onLLMSelect={handleExecutionLLMSelect}
                    onRefresh={loadDefaultsFromBackend}
                    inModal={false}
                    openDirection="down"
                  />
                </div>

	              </div>
                  {agentConfigs.execution_llm?.provider && ['claude-code','codex-cli','cursor-cli','pi-cli','muse-cli'].includes(agentConfigs.execution_llm.provider) && <ProviderAccounts key={agentConfigs.execution_llm.provider} provider={agentConfigs.execution_llm.provider} selectedId={agentConfigs.execution_llm.connection_id} disabled={isSaving} onSelect={connection_id=>setAgentConfigs(prev=>({...prev,execution_llm:{...prev.execution_llm,connection_id}}))} />}
	              {presetLLMConfig?.mode === 'explicit' && (
	                <div>
	                  <label className="text-xs text-gray-600 dark:text-gray-400">Execution Tier</label>
	                  <p className="text-[10px] text-gray-500 dark:text-gray-500 mb-1">
	                    Reviewed by Pulse Architect using quality, cost, and latency evidence
	                  </p>
	                  {agentConfigs.execution_llm?.provider && agentConfigs.execution_llm?.model_id ? (
	                    <p className="text-[10px] text-amber-600 dark:text-amber-400 mt-1">
	                      ⚠️ Execution LLM is set — it takes precedence over execution tier.
	                    </p>
	                  ) : (
	                    /* Tier changes carry evidence and are applied through Pulse decisions.
                         Display configured state without guessing whether a user or reviewer set it. */
	                    <p className="text-xs text-gray-900 dark:text-gray-100">
	                      {agentConfigs.execution_tier
	                        ? `${agentConfigs.execution_tier} (configured)`
	                        : 'High (default)'}
	                    </p>
	                  )}
	                </div>
	              )}
	            </div>

            {/* LLM Validation Agent Configuration — disabled (LLM validation is dead code in backend).
               Only pre-validation (code-based structural checks) is active. Kept commented out for reference. */}

            {/* MCP Servers and Tools Selection (Step Level) */}
            {presetServers.length > 0 && (
              <>
                <div className="border-t border-gray-200 dark:border-gray-700"></div>
                <div className="space-y-2">
                  <div className="text-xs font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">
                    MCP Servers & Tools (Step Level)
                  </div>
                  <div className="text-xs text-gray-500 dark:text-gray-500 italic">
                    Filter preset's selected servers/tools further for this step
                  </div>
                  
                  {/* NO_SERVERS Option */}
                  <div className="flex items-center gap-2 p-2 bg-gray-100 dark:bg-gray-800/50 rounded border border-gray-200 dark:border-gray-700">
                    <input
                      type="checkbox"
                      id={`no-servers-${stepIndex}`}
                      checked={hasNoServers}
                      onChange={(e) => {
                        if (e.target.checked) {
                          // Set NO_SERVERS and clear all other selections
                          setSelectedServers(["NO_SERVERS"]);
                          setSelectedTools([]);
                        } else {
                          // Remove NO_SERVERS and use preset defaults
                          setSelectedServers(presetServers);
                          setSelectedTools(currentPresetTools || []);
                        }
                      }}
                      className="w-4 h-4 text-blue-600 border-gray-300 rounded focus:ring-blue-500"
                    />
                    <label
                      htmlFor={`no-servers-${stepIndex}`}
                      className="text-xs text-gray-700 dark:text-gray-300 cursor-pointer flex-1"
                    >
                      <span className="font-medium">No MCP Servers</span>
                      <span className="text-gray-500 dark:text-gray-500 ml-1">
                        (Pure LLM mode - no tools available)
                      </span>
                    </label>
                  </div>

                  {/* Server/Tool Selection (disabled when NO_SERVERS is selected) */}
                  {!hasNoServers && (
                    <ToolSelectionSection
                      availableServers={presetServers}
                      selectedServers={actualSelectedServers}
                      selectedTools={selectedTools}
                      onServerChange={(servers) => {
                        // Ensure NO_SERVERS is not included
                        setSelectedServers(servers.filter(s => s !== "NO_SERVERS"));
                      }}
                      onToolChange={setSelectedTools}
                      stepId={step.id}
                      agentMode="workflow"
                    />
                  )}

                </div>
              </>
            )}

            {/* Custom Tool Categories and Individual Tools Selection */}
            <div className="border-t border-gray-200 dark:border-gray-700"></div>
            <div className="space-y-3">
              <div className="text-xs font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">
                Custom Tools
              </div>
              <div className="text-xs text-gray-500 dark:text-gray-500 italic">
                Select categories (enables all tools) or individual tools. By default, all tools are enabled.
              </div>
              
              {/* Workspace Tools Category */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <label className="flex items-center gap-2 cursor-pointer flex-1">
                    <input
                      type="checkbox"
                      checked={(() => {
                        const allWorkspaceTools = getToolsByCategory('workspace_tools', capabilities?.workspace);
                        if (enabledCustomTools.length === 0) return true;
                        if (isCategoryEnabled('workspace_tools', enabledCustomTools)) return true;
                        const enabledCount = allWorkspaceTools.filter(t => {
                          const category = getCategoryForTool(t) || 'workspace_tools';
                          return isToolEnabled(category, t, enabledCustomTools);
                        }).length;
                        return enabledCount === allWorkspaceTools.length;
                      })()}
                      onChange={(e) => {
                        if (e.target.checked) {
                          setEnabledCustomTools(prev => enableCategory('workspace_tools', prev));
                        } else {
                          setEnabledCustomTools(prev => disableCategory('workspace_tools', prev));
                        }
                      }}
                      className="w-4 h-4 text-blue-600 border-gray-300 rounded focus:ring-blue-500"
                    />
                    <span className="text-xs font-medium text-gray-700 dark:text-gray-300">Workspace Tools</span>
                    <span className="text-xs text-gray-500 dark:text-gray-500">
                      {(() => {
                        const allWorkspaceTools = getToolsByCategory('workspace_tools', capabilities?.workspace);
                        if (enabledCustomTools.length === 0) return `(${allWorkspaceTools.length}/${allWorkspaceTools.length} tools)`;
                        if (isCategoryEnabled('workspace_tools', enabledCustomTools)) return `(${allWorkspaceTools.length}/${allWorkspaceTools.length} tools)`;
                        const enabledCount = allWorkspaceTools.filter(t => {
                          const category = getCategoryForTool(t) || 'workspace_tools';
                          return isToolEnabled(category, t, enabledCustomTools);
                        }).length;
                        return `(${enabledCount}/${allWorkspaceTools.length} tools)`;
                      })()}
                    </span>
                  </label>
                  <button
                    type="button"
                    onClick={() => {
                      const newExpanded = new Set(expandedToolCategories);
                      if (newExpanded.has('workspace_tools')) {
                        newExpanded.delete('workspace_tools');
                      } else {
                        newExpanded.add('workspace_tools');
                      }
                      setExpandedToolCategories(newExpanded);
                    }}
                    className="text-xs text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300"
                  >
                    {expandedToolCategories.has('workspace_tools') ? 'Hide' : 'Show'} tools
                  </button>
                </div>
                {expandedToolCategories.has('workspace_tools') && (
                  <div className="ml-6 space-y-1.5 pl-2 border-l-2 border-gray-200 dark:border-gray-700">
                    {getToolsByCategory('workspace_tools', capabilities?.workspace).map((toolName: string) => {
                      const toolCategory = getCategoryForTool(toolName) || 'workspace_tools';
                      const toolIsEnabled = isToolEnabled(toolCategory, toolName, enabledCustomTools);
                      return (
                        <label key={toolName} className="flex items-center gap-2 cursor-pointer">
                          <input
                            type="checkbox"
                            checked={toolIsEnabled}
                            onChange={(e) => {
                              if (e.target.checked) {
                                setEnabledCustomTools(prev => enableTool(toolCategory, toolName, prev));
                              } else {
                                setEnabledCustomTools(prev => disableTool(toolCategory, toolName, prev));
                              }
                            }}
                            className="w-4 h-4 text-blue-600 border-gray-300 rounded focus:ring-blue-500"
                          />
                          <span className="text-xs text-gray-600 dark:text-gray-400">{toolName}</span>
                        </label>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* Human Tools Category */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <label className="flex items-center gap-2 cursor-pointer flex-1">
                    <input
                      type="checkbox"
                      checked={(() => {
                        const allHumanTools = getToolsByCategory('human_tools', capabilities?.workspace);
                        const categoryEnabled = isCategoryEnabled('human_tools', enabledCustomTools);
                        
                        if (categoryEnabled) return true;
                        
                        // Check if all human tools are enabled individually
                        const humanSpecificTools = enabledCustomTools
                          .map(entry => parseToolEntry(entry))
                          .filter(parsed => parsed && parsed.category === 'human_tools' && parsed.tool !== '*')
                          .map(parsed => parsed!.tool);
                        
                        // Checked if all tools are enabled (either via category:* or all individual tools)
                        return humanSpecificTools.length === allHumanTools.length;
                      })()}
                      onChange={(e) => {
                        if (e.target.checked) {
                          setEnabledCustomTools(prev => enableCategory('human_tools', prev));
                        } else {
                          setEnabledCustomTools(prev => disableCategory('human_tools', prev));
                        }
                      }}
                      className="w-4 h-4 text-blue-600 border-gray-300 rounded focus:ring-blue-500"
                    />
                    <span className="text-xs font-medium text-gray-700 dark:text-gray-300">Human Tools</span>
                    <span className="text-xs text-gray-500 dark:text-gray-500">
                      {(() => {
                        const allHumanTools = getToolsByCategory('human_tools', capabilities?.workspace);
                        const categoryEnabled = isCategoryEnabled('human_tools', enabledCustomTools);
                        
                        let enabledCount = 0;
                        if (categoryEnabled || enabledCustomTools.length === 0) {
                          enabledCount = allHumanTools.length;
                        } else {
                          const humanSpecificTools = enabledCustomTools
                            .map(entry => parseToolEntry(entry))
                            .filter(parsed => parsed && parsed.category === 'human_tools' && parsed.tool !== '*')
                            .map(parsed => parsed!.tool);
                          enabledCount = humanSpecificTools.length;
                        }
                        
                        return `(${enabledCount}/${allHumanTools.length} tools)`;
                      })()}
                    </span>
                  </label>
                  <button
                    type="button"
                    onClick={() => {
                      const newExpanded = new Set(expandedToolCategories);
                      if (newExpanded.has('human_tools')) {
                        newExpanded.delete('human_tools');
                      } else {
                        newExpanded.add('human_tools');
                      }
                      setExpandedToolCategories(newExpanded);
                    }}
                    className="text-xs text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300"
                  >
                    {expandedToolCategories.has('human_tools') ? 'Hide' : 'Show'} tools
                  </button>
                </div>
                {expandedToolCategories.has('human_tools') && (
                  <div className="ml-6 space-y-1.5 pl-2 border-l-2 border-gray-200 dark:border-gray-700">
                    {HUMAN_TOOLS.map((toolName) => {
                      const toolIsEnabled = isToolEnabled('human_tools', toolName, enabledCustomTools);
                      
                      return (
                        <label key={toolName} className="flex items-center gap-2 cursor-pointer">
                          <input
                            type="checkbox"
                            checked={toolIsEnabled}
                            onChange={(e) => {
                              if (e.target.checked) {
                                setEnabledCustomTools(prev => enableTool('human_tools', toolName, prev));
                              } else {
                                setEnabledCustomTools(prev => disableTool('human_tools', toolName, prev));
                              }
                            }}
                            className="w-4 h-4 text-blue-600 border-gray-300 rounded focus:ring-blue-500"
                          />
                          <span className="text-xs text-gray-600 dark:text-gray-400">{toolName}</span>
                        </label>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            {/* Parallel Tool Execution Toggle */}
            <div className="border-t border-gray-200 dark:border-gray-700"></div>
            <div className="space-y-2">
              <div className="text-xs font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">
                Parallel Tool Execution
              </div>
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  id={`parallel-tool-exec-${stepIndex}`}
                  checked={agentConfigs.disable_parallel_tool_execution !== true}
                  onChange={(e) => {
                    setAgentConfigs((prev) => ({
                      ...prev,
                      disable_parallel_tool_execution: !e.target.checked || undefined,
                    }));
                  }}
                  className="w-4 h-4 text-blue-600 border-gray-300 rounded focus:ring-blue-500"
                />
                <label
                  htmlFor={`parallel-tool-exec-${stepIndex}`}
                  className="text-xs text-gray-600 dark:text-gray-400 cursor-pointer flex-1"
                >
                  Enable Parallel Tool Execution
                  <span className="text-gray-500 dark:text-gray-500 ml-1">
                    (allows concurrent execution of multiple independent tool calls)
                  </span>
                </label>
              </div>
            </div>

            {/* Action Buttons */}
            <div className="flex items-center justify-end gap-2 pt-2 border-t border-gray-200 dark:border-gray-700">
              <Button
                variant="default"
                size="sm"
                onClick={handleSave}
                disabled={isSaving}
                className="text-xs h-7 px-3"
              >
                {isSaving ? 'Saving...' : 'Save'}
              </Button>
            </div>
          </div>
        </div>
      )}
        </>
      )}
    </div>
  );
};

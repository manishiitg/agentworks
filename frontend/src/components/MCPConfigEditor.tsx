import React, { useState, useEffect } from 'react';
import { Loader2, Save, RefreshCw, CheckCircle, XCircle, AlertTriangle, Settings, Lock } from 'lucide-react';
import { mcpConfigApi, type MCPConfigStatus } from '../services/mcpConfigApi';

interface MCPConfigEditorProps {
  onConfigChange?: () => void;
  onClose?: () => void;
}

export const MCPConfigEditor: React.FC<MCPConfigEditorProps> = ({
  onConfigChange,
}) => {
  const [configJson, setConfigJson] = useState<string>('');
  const [originalJson, setOriginalJson] = useState<string>('');
  const [status, setStatus] = useState<MCPConfigStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [hasChanges, setHasChanges] = useState(false);
  const [isLocked, setIsLocked] = useState(false);
  const [customServerCount, setCustomServerCount] = useState(0);

  // Load initial config
  useEffect(() => {
    loadConfig();
    loadStatus();
  }, []);

  // Check for changes
  useEffect(() => {
    const changed = configJson !== originalJson;
    setHasChanges(changed);
  }, [configJson, originalJson]);

  const loadConfig = async () => {
    try {
      setLoading(true);
      setError(null);
      
      const data = await mcpConfigApi.getConfig();
      // The response carries mcp_config_locked alongside the servers. That is
      // transport metadata, not something anyone should be editing, so only the
      // mcpServers document reaches the textarea.
      const servers =
        (data as { mcpServers?: Record<string, unknown> })?.mcpServers ?? {};
      const jsonString = JSON.stringify({ mcpServers: servers }, null, 2);
      setConfigJson(jsonString);
      setOriginalJson(jsonString);
      setCustomServerCount(Object.keys(servers).length);
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Failed to load config');
    } finally {
      setLoading(false);
    }
  };

  const loadStatus = async () => {
    try {
      const data = await mcpConfigApi.getStatus();
      setStatus(data);
      setIsLocked(data.mcp_config_locked ?? false);
    } catch (error) {
      console.error('Failed to load status:', error);
    }
  };

  const saveConfig = async () => {
    try {
      setSaving(true);
      setError(null);
      setSuccess(null);

      // Parsing and saving fail for different reasons, so they are caught
      // separately. Wrapping both in one catch meant a server rejection -- a
      // reserved connector name, say -- was reported as "Invalid JSON format".
      let parsed: { mcpServers?: Record<string, unknown> };
      try {
        parsed = JSON.parse(configJson);
      } catch {
        setJsonError('Invalid JSON format');
        return;
      }
      setJsonError(null);

      // Accept the "official" { "servers": ... } shape people paste from docs.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      if ((parsed as any).servers && !parsed.mcpServers) {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        parsed.mcpServers = (parsed as any).servers;
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        delete (parsed as any).servers;
        setConfigJson(JSON.stringify(parsed, null, 2));
      }
      if (!parsed.mcpServers) {
        setError('Missing "mcpServers" — the config must be { "mcpServers": { ... } }.');
        return;
      }

      const count = Object.keys(parsed.mcpServers).length;
      const result = await mcpConfigApi.saveConfig(parsed);
      setCustomServerCount(result.servers ?? count);
      setOriginalJson(configJson);
      setHasChanges(false);

      // Start discovery process
      setDiscovering(true);
      setSuccess(
        count === 0
          ? 'Saved. You have no custom MCP servers.'
          : `Saved ${count} custom MCP server${count === 1 ? '' : 's'}. Discovering...`
      );

      await mcpConfigApi.discoverServers();
      await new Promise(resolve => setTimeout(resolve, 20000));
      await loadStatus();

      setSuccess('Servers discovered successfully!');
      await new Promise(resolve => setTimeout(resolve, 1000));

      onConfigChange?.();
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Failed to save config');
    } finally {
      setSaving(false);
      setDiscovering(false);
    }
  };

  const discoverServers = async () => {
    try {
      setDiscovering(true);
      setError(null);
      setSuccess(null);

      await mcpConfigApi.discoverServers();
      setSuccess('Server discovery started in background...');
      
      // Reload status after a short delay
      setTimeout(() => {
        loadStatus();
      }, 2000);
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Failed to start discovery');
    } finally {
      setDiscovering(false);
    }
  };

  const handleJsonChange = (value: string) => {
    setConfigJson(value);
    // Validate JSON in real-time
    try {
      JSON.parse(value);
      setJsonError(null);
    } catch {
      setJsonError('Invalid JSON format');
    }
  };

  const formatJson = () => {
    try {
      const parsed = JSON.parse(configJson);
      const formatted = JSON.stringify(parsed, null, 2);
      setConfigJson(formatted);
      setJsonError(null);
    } catch {
      setJsonError('Cannot format invalid JSON');
    }
  };

  const resetConfig = () => {
    setConfigJson(originalJson);
    setError(null);
    setSuccess(null);
    setJsonError(null);
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center p-8">
        <Loader2 className="h-8 w-8 animate-spin text-gray-500 dark:text-gray-400" />
        <span className="ml-2 text-gray-600 dark:text-gray-400">Loading MCP configuration...</span>
      </div>
    );
  }

  // Show locked message when MCP config is locked by admin
  if (isLocked) {
    return (
      <div className="space-y-6">
        {/* Header with Lock Icon */}
        <div className="flex items-center justify-between mb-6">
          <div className="flex items-center gap-3">
            <Lock className="h-6 w-6 text-amber-500" />
            <div>
              <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100">MCP Server Configuration</h2>
              <p className="text-sm text-amber-600 dark:text-amber-400 mt-1">
                Configuration is locked by administrator
              </p>
            </div>
          </div>
        </div>

        {/* Locked Message */}
        <div className="p-4 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-lg">
          <div className="flex items-start gap-3">
            <Lock className="h-5 w-5 text-amber-500 mt-0.5 flex-shrink-0" />
            <div>
              <p className="text-amber-800 dark:text-amber-200 font-medium">
                MCP server configuration is on Run
              </p>
              <p className="text-sm text-amber-700 dark:text-amber-300 mt-1">
                Contact your administrator to add, edit, or remove MCP servers.
              </p>
            </div>
          </div>
        </div>

        {/* Status Cards (read-only) */}
        {status && (
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
              <div className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Total Servers</div>
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">{status.total_servers}</div>
            </div>
            <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
              <div className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Discovered Servers</div>
              <div className="text-2xl font-bold text-green-600 dark:text-green-400">{status.discovered_servers}</div>
            </div>
            <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
              <div className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Discovery Status</div>
              <div className="flex items-center gap-2">
                {status.discovery_running ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin text-blue-500" />
                    <span className="text-sm text-gray-900 dark:text-gray-100">Running</span>
                  </>
                ) : (
                  <>
                    <CheckCircle className="h-4 w-4 text-green-600 dark:text-green-400" />
                    <span className="text-sm text-gray-900 dark:text-gray-100">Idle</span>
                  </>
                )}
              </div>
            </div>
          </div>
        )}

        {/* Read-only JSON view */}
        <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
          <div className="mb-4">
            <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">Current Configuration</h3>
            <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
              View-only mode — editing is disabled
            </p>
          </div>
          <textarea
            value={configJson}
            readOnly
            className="w-full h-64 p-4 border border-gray-300 dark:border-gray-600 rounded-lg font-mono text-sm resize-none bg-gray-100 dark:bg-gray-800 text-gray-700 dark:text-gray-300 cursor-not-allowed"
          />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100">MCP Server Configuration</h2>
          <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
            Add your own MCP servers as JSON. Built-in connectors are managed
            from the connector directory.
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={loadConfig}
            disabled={loading}
            className="px-3 py-2 text-sm bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 rounded-md transition-colors flex items-center gap-2 disabled:opacity-50"
          >
            <RefreshCw className="h-4 w-4" />
            Reload
          </button>
        </div>
      </div>

      {/* Status Cards */}
      {status && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
            <div className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Total Servers</div>
            <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">{status.total_servers}</div>
          </div>
          <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
            <div className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Discovered Servers</div>
            <div className="text-2xl font-bold text-green-600 dark:text-green-400">{status.discovered_servers}</div>
          </div>
          <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
            <div className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Discovery Status</div>
            <div className="flex items-center gap-2">
              {status.discovery_running ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin text-blue-500" />
                  <span className="text-sm text-gray-900 dark:text-gray-100">Running</span>
                </>
              ) : (
                <>
                  <CheckCircle className="h-4 w-4 text-green-600 dark:text-green-400" />
                  <span className="text-sm text-gray-900 dark:text-gray-100">Idle</span>
                </>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Alerts */}
      {error && (
        <div className="p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-md">
          <div className="flex items-center gap-2">
            <XCircle className="h-4 w-4 text-red-500" />
            <span className="text-sm text-red-700 dark:text-red-400">{error}</span>
          </div>
        </div>
      )}

      {success && (
        <div className="p-3 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded-md">
          <div className="flex items-center gap-2">
            <CheckCircle className="h-4 w-4 text-green-500" />
            <span className="text-sm text-green-700 dark:text-green-400">{success}</span>
          </div>
        </div>
      )}

      {discovering && (
        <div className="p-3 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-md">
          <div className="flex items-center gap-2">
            <Loader2 className="h-4 w-4 animate-spin text-blue-500" />
            <span className="text-sm text-blue-700 dark:text-blue-400">
              Discovering servers... This may take a few moments.
            </span>
          </div>
        </div>
      )}

      {jsonError && (
        <div className="p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-md">
          <div className="flex items-center gap-2">
            <AlertTriangle className="h-4 w-4 text-red-500" />
            <span className="text-sm text-red-700 dark:text-red-400">{jsonError}</span>
          </div>
        </div>
      )}


      {/* JSON Editor */}
      <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">JSON Configuration</h3>
            <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
              {customServerCount === 0
                ? 'You have not added any custom servers yet'
                : `Your ${customServerCount} custom server${customServerCount === 1 ? '' : 's'}`}
            </p>
          </div>
          <div className="flex gap-2">
            <button
              onClick={formatJson}
              className="px-3 py-2 text-sm bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 rounded-md transition-colors flex items-center gap-2"
            >
              <Settings className="h-4 w-4" />
              Format JSON
            </button>
          </div>
        </div>
        
        {/* Help Note */}
        <div className="mb-4 p-3 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-md">
          <div className="flex items-start gap-2">
            <AlertTriangle className="h-4 w-4 text-blue-500 mt-0.5 flex-shrink-0" />
            <div className="text-sm text-blue-700 dark:text-blue-300">
              <strong>This is your own configuration.</strong> It lists only the
              servers you have added, and saving replaces exactly that list —
              removing an entry here deletes that server. The{' '}
              {status?.base_servers ?? 'built-in'} connectors in the directory
              are not shown and cannot be edited or deleted from here; connect
              or disconnect those from the directory instead.
              <div className="mt-2 font-mono text-xs opacity-80">
                {'{ "mcpServers": { "my-server": { "url": "https://example.com/mcp" } } }'}
              </div>
            </div>
          </div>
        </div>
        <textarea
          value={configJson}
          onChange={(e) => handleJsonChange(e.target.value)}
          className="w-full h-96 p-4 border border-gray-300 dark:border-gray-600 rounded-lg font-mono text-sm resize-none focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100"
          placeholder="Enter JSON configuration..."
        />
      </div>

      {/* Actions */}
      <div className="flex items-center justify-between">
        <div className="flex gap-2">
          <button
            onClick={discoverServers}
            disabled={discovering || saving}
            className="px-4 py-2 text-sm bg-blue-50 dark:bg-blue-800 hover:bg-blue-100 dark:hover:bg-blue-700 text-blue-700 dark:text-blue-300 rounded-md transition-colors flex items-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {discovering ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RefreshCw className="h-4 w-4" />
            )}
            Discover Servers
          </button>
        </div>
        <div className="flex gap-2">
          {hasChanges && (
            <button
              onClick={resetConfig}
              className="px-4 py-2 text-sm bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-700 dark:text-gray-300 rounded-md transition-colors"
            >
              Reset Changes
            </button>
          )}
          <button
            onClick={saveConfig}
            disabled={saving || discovering || !hasChanges || !!jsonError}
            className="px-4 py-2 text-sm bg-blue-500 hover:bg-blue-600 text-white rounded-md transition-colors flex items-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {saving ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : discovering ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Save className="h-4 w-4" />
            )}
            {saving ? 'Saving...' : discovering ? 'Discovering...' : 'Save Configuration'}
          </button>
        </div>
      </div>
    </div>
  );
};

export default MCPConfigEditor;

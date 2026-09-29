/**
 * OAuth Status Badge Component
 * Shows OAuth authentication status for MCP servers
 */

import React, { useState, useEffect, useRef } from 'react';
import { Loader2, Key, X, Plus, Trash2 } from 'lucide-react';
import { oauthApi } from '../services/oauthApi';
import type { OAuthDiscoveryResponse } from '../services/oauthApi';
import { mcpConfigApi } from '../services/mcpConfigApi';
import { useChatStore } from '../stores';
import { useAuthStore } from '../stores/useAuthStore';
import { READ_ONLY_TITLE } from '../hooks/useCanWriteWorkflow';

/** Toasts are global, so read the action lazily rather than subscribing to the store. */
const notify = (message: string, type: 'success' | 'info' | 'error') =>
  useChatStore.getState().addToast(message, type);

interface OAuthStatusBadgeProps {
  serverName: string;
  requiresOAuth?: boolean; // Read from server config (presence of an oauth block)
  /**
   * Connection ownership from /api/tools — 'connected' | 'available'. When
   * supplied, the control is driven by this instead of by polling
   * /api/oauth/status, and clicks route to the connect/disconnect endpoints.
   * When omitted, the component behaves exactly as before.
   */
  connection?: string;
  onAuthChange?: (valid: boolean) => void;
  /**
   * `label` renders the wide "Connect"/"Disconnect" buttons used by the server
   * dropdown and details modal. `icon` renders the compact square control the
   * connector directory cards use, where the name and description already say
   * which service the button belongs to.
   */
  variant?: 'label' | 'icon';
  /** The current user can't change workflow state: connect/disconnect and
   * the credential dialogs' submit buttons disable. */
  readOnly?: boolean;
}

export const OAuthStatusBadge: React.FC<OAuthStatusBadgeProps> = ({
  serverName,
  requiresOAuth,
  connection,
  onAuthChange,
  variant = 'label',
  readOnly = false,
}) => {
  const canManagePlatformMCP = useAuthStore(state =>
    state.user?.is_admin === true || (state.isMultiUserModeChecked && !state.isMultiUserMode),
  );
  const effectiveReadOnly = readOnly || !canManagePlatformMCP;
  const readOnlyTitle = !canManagePlatformMCP
    ? 'Only a platform administrator can connect or disconnect this shared service'
    : READ_ONLY_TITLE;

  // When the caller knows the connection state, this component stops asking the
  // server about it — that is what removes ~24 polls per 10s from the directory.
  const connectionDriven = connection !== undefined;
  const [tokenValid, setTokenValid] = useState<boolean>(false);
  const [loading, setLoading] = useState(false);
  const [hasOAuth, setHasOAuth] = useState<boolean | null>(null);
  const prevTokenValidRef = useRef<boolean | null>(null);

  // Connection ownership when supplied, token validity otherwise.
  const isConnected = connectionDriven ? connection === 'connected' : tokenValid;

  // Dialog state. 'client_id' collects an OAuth client_id for servers without
  // DCR; 'api_key' collects an optional bearer key for open servers.
  const [dialogMode, setDialogMode] = useState<'client_id' | 'api_key' | null>(null);
  const [clientIdInput, setClientIdInput] = useState('');
  const [clientSecretInput, setClientSecretInput] = useState('');
  const [apiKeyInput, setApiKeyInput] = useState('');
  const [discoveryInfo, setDiscoveryInfo] = useState<OAuthDiscoveryResponse | null>(null);
  const showClientIdDialog = dialogMode === 'client_id';

  // Check token status on mount and periodically
  const checkTokenStatus = React.useCallback(async () => {
    try {
      console.log(`[OAuthStatusBadge] Checking status for ${serverName}...`);
      const status = await oauthApi.getOAuthStatus(serverName);
      console.log(`[OAuthStatusBadge] Status for ${serverName}:`, status);

      // Trigger refresh when auth becomes valid, including the first status
      // check after a page remount or missed polling transition.
      const prevValid = prevTokenValidRef.current;
      const becameValid = status.valid && prevValid !== true;

      setTokenValid(status.valid);
      // An open server now answers 200 with has_oauth:false rather than erroring,
      // so honour it explicitly — otherwise the label variant would start
      // offering a Connect button for servers that have no OAuth at all.
      setHasOAuth(status.has_oauth !== false);
      prevTokenValidRef.current = status.valid;

      if (becameValid) {
        console.log(`[OAuthStatusBadge] Auth changed to valid for ${serverName}, triggering refresh`);
        onAuthChange?.(status.valid);
      }
    } catch (error) {
      console.log(`[OAuthStatusBadge] Error checking status for ${serverName}:`, error);
      // If not explicitly told server has OAuth, check if auto-detected
      if (requiresOAuth === undefined) {
        setHasOAuth(false);
      }
      setTokenValid(false);
      prevTokenValidRef.current = false;
    }
  }, [serverName, requiresOAuth, onAuthChange]);

  useEffect(() => {
    // If requiresOAuth is explicitly passed (read from config), use it immediately
    if (requiresOAuth !== undefined) {
      setHasOAuth(requiresOAuth);
    }

    // The connection prop already carries the answer; polling would only
    // re-derive it, once per card every 10 seconds.
    if (connectionDriven) return;

    checkTokenStatus();
    const interval = setInterval(checkTokenStatus, 10000); // Every 10 seconds
    return () => clearInterval(interval);
  }, [serverName, requiresOAuth, connectionDriven, checkTokenStatus]);

  const handleLogin = async (clientId?: string, clientSecret?: string) => {
    if (!clientId && !window.confirm(
      `Connect ${serverName} as a shared AgentWorks connection? All users, Work projects, workflows, chats, and schedules will be able to use this authenticated account.`,
    )) return;
    setLoading(true);
    console.log(`[OAuthStatusBadge] Starting OAuth login for ${serverName}${clientId ? ' with client_id' : ''}`);
    try {
      // Start OAuth flow and get authorization URL
      const response = await oauthApi.startOAuthFlow(serverName, clientId, clientSecret);
      console.log(`[OAuthStatusBadge] OAuth flow response for ${serverName}:`, response);

      // Check if the server needs a client_id
      if ('status' in response && response.status === 'needs_client_id') {
        console.log(`[OAuthStatusBadge] Server ${serverName} needs client_id`);
        setDiscoveryInfo(response as OAuthDiscoveryResponse);
        setDialogMode('client_id');
        setLoading(false);
        return;
      }

      // Normal flow - open browser with authorization URL
      const startResponse = response as { auth_url: string; state: string };
      if (startResponse.auth_url) {
        console.log(`[OAuthStatusBadge] Opening auth URL for ${serverName}`);
        window.open(startResponse.auth_url, '_blank');
      }

      // Poll for completion
      let pollCount = 0;
      const pollInterval = setInterval(async () => {
        pollCount++;
        try {
          console.log(`[OAuthStatusBadge] Polling OAuth status for ${serverName} (attempt ${pollCount})`);
          const status = await oauthApi.getOAuthStatus(serverName);
          console.log(`[OAuthStatusBadge] Poll result for ${serverName}:`, status);
          if (status.valid) {
            console.log(`[OAuthStatusBadge] OAuth completed for ${serverName}!`);
            clearInterval(pollInterval);
            const wasInvalid = prevTokenValidRef.current === false || prevTokenValidRef.current === null;
            setTokenValid(true);
            setLoading(false);
            prevTokenValidRef.current = true;
            notify(`Connected ${serverName} across AgentWorks`, 'success');
            // Only trigger refresh if transitioning from invalid to valid
            if (wasInvalid) {
              console.log(`[OAuthStatusBadge] Triggering onAuthChange for ${serverName}`);
              onAuthChange?.(true);
            }
          }
        } catch (err) {
          console.log(`[OAuthStatusBadge] Poll error for ${serverName}:`, err);
          // Still waiting
        }
      }, 2000);

      // Stop polling after 5 minutes
      setTimeout(() => {
        console.log(`[OAuthStatusBadge] Polling timeout for ${serverName}`);
        clearInterval(pollInterval);
        setLoading(false);
      }, 5 * 60 * 1000);
    } catch (error) {
      console.error('[OAuthStatusBadge] OAuth login failed:', error);
      notify(`Could not connect to ${serverName}`, 'error');
      setLoading(false);
    }
  };

  const handleClientIdSubmit = () => {
    if (!clientIdInput.trim() || (discoveryInfo?.needs_client_secret && !clientSecretInput.trim())) return;
    setDialogMode(null);
    setDiscoveryInfo(null);
    handleLogin(clientIdInput.trim(), clientSecretInput.trim() || undefined);
    setClientIdInput('');
    setClientSecretInput('');
  };

  const handleClientIdCancel = () => {
    setDialogMode(null);
    setClientIdInput('');
    setClientSecretInput('');
    setDiscoveryInfo(null);
  };

  /** Connect an open server, with or without an API key. */
  const handleConnect = async (apiKey?: string) => {
    setLoading(true);
    try {
      const response = await mcpConfigApi.connectServer(serverName, apiKey);
      // The server has the final say on whether OAuth is required; fall through
      // to the authorization flow rather than reporting a false success.
      if (response.status === 'oauth_required') {
        setLoading(false);
        handleLogin();
        return;
      }
      notify(`Connected ${serverName} across AgentWorks`, 'success');
      onAuthChange?.(true);
    } catch (error) {
      console.error('[OAuthStatusBadge] Connect failed:', error);
      notify(`Could not connect to ${serverName}`, 'error');
    } finally {
      setLoading(false);
    }
  };

  const handleDisconnect = async () => {
    if (!window.confirm(`Disconnect the shared ${serverName} connection? Work, workflows, chats, and schedules across AgentWorks will lose access until an administrator reconnects it.`)) return;
    setLoading(true);
    try {
      await mcpConfigApi.disconnectServer(serverName);
      setTokenValid(false);
      prevTokenValidRef.current = false;
      notify(`Disconnected from ${serverName}`, 'info');
      onAuthChange?.(false);
    } catch (error) {
      console.error('[OAuthStatusBadge] Disconnect failed:', error);
      notify(`Could not disconnect from ${serverName}`, 'error');
    } finally {
      setLoading(false);
    }
  };

  /** Entry point for the connection-driven control. */
  const handleConnectClick = () => {
    if (requiresOAuth) {
      handleLogin();
      return;
    }
    if (!window.confirm(
      `Connect ${serverName} as a shared AgentWorks connection? All users, Work projects, workflows, chats, and schedules will be able to use it.`,
    )) return;
    // Open servers may accept an optional key; ask before connecting.
    setDialogMode('api_key');
  };

  const handleApiKeySubmit = () => {
    const key = apiKeyInput.trim();
    setDialogMode(null);
    setApiKeyInput('');
    handleConnect(key || undefined);
  };

  const handleApiKeyCancel = () => {
    setDialogMode(null);
    setApiKeyInput('');
  };

  const handleLogout = async () => {
    if (!window.confirm(`Disconnect the shared ${serverName} connection? AgentWorks users and scheduled workflows will lose access until an administrator reconnects it.`)) return;
    setLoading(true);
    try {
      await oauthApi.logout(serverName);
      setTokenValid(false);
      prevTokenValidRef.current = false;
      notify(`Disconnected from ${serverName}`, 'info');
      onAuthChange?.(false);
    } catch (error) {
      console.error('OAuth logout failed:', error);
      notify(`Could not disconnect from ${serverName}`, 'error');
    } finally {
      setLoading(false);
    }
  };

  // Don't render if server doesn't have OAuth. In connection-driven mode this
  // gate does not apply — an open server is exactly the case that must still
  // render a Connect button, and it is why open servers previously showed none.
  if (hasOAuth === false && !connectionDriven) {
    return null;
  }

  // Loading state while checking
  if (hasOAuth === null && !connectionDriven) {
    return (
      <div className="flex items-center gap-1 px-2 py-1 text-xs bg-gray-100 dark:bg-gray-700 rounded">
        <Loader2 className="w-3 h-3 animate-spin" />
      </div>
    );
  }

  // Client ID dialog modal
  const clientIdDialog = showClientIdDialog && (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 max-w-md w-full mx-4">
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <Key className="w-5 h-5 text-orange-500" />
            <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
              OAuth App Credentials Required
            </h3>
          </div>
          <button
            onClick={handleClientIdCancel}
            className="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 rounded"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <p className="text-sm text-gray-600 dark:text-gray-400 mb-4">
          {discoveryInfo?.message || `This server does not support automatic client registration. Please enter your OAuth App client ID.`}
        </p>

        <p className="mb-4 rounded-md bg-blue-50 p-2 text-xs text-blue-700 dark:bg-blue-900/20 dark:text-blue-300">
          This OAuth app and its connected account are shared across AgentWorks, including Work, workflows, chats, and schedules.
        </p>


        {discoveryInfo?.scopes_supported && discoveryInfo.scopes_supported.length > 0 && (
          <div className="mb-4 p-2 bg-blue-50 dark:bg-blue-900/20 rounded text-xs text-blue-700 dark:text-blue-300">
            <span className="font-medium">Discovered scopes:</span>{' '}
            {discoveryInfo.scopes_supported.join(', ')}
          </div>
        )}

        {discoveryInfo?.resource && (
          <div className="mb-4 p-2 bg-gray-50 dark:bg-gray-700/50 rounded text-xs text-gray-600 dark:text-gray-400">
            <span className="font-medium">Resource:</span> {discoveryInfo.resource}
          </div>
        )}

        <div className="mb-4">
          <label htmlFor="client-id-input" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
            Client ID
          </label>
          <input
            id="client-id-input"
            type="text"
            value={clientIdInput}
            onChange={(e) => setClientIdInput(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleClientIdSubmit()}
            placeholder="Enter your OAuth client_id"
            className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md text-sm bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            autoFocus
          />
          <label htmlFor="client-secret-input" className="mt-3 block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
            Client secret <span className="font-normal text-gray-500">(if the provider issued one, as Google and GitHub do)</span>
          </label>
          <input
            id="client-secret-input"
            type="password"
            autoComplete="off"
            value={clientSecretInput}
            onChange={(e) => setClientSecretInput(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleClientIdSubmit()}
            className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md text-sm bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
          />
          {discoveryInfo?.redirect_uri && (
            <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
              Register this callback URL on the OAuth app: <code className="break-all">{discoveryInfo.redirect_uri}</code>
            </p>
          )}
        </div>


        <div className="flex justify-end gap-2">
          <button
            onClick={handleClientIdCancel}
            className="px-4 py-2 text-sm text-gray-600 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200 rounded-md transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleClientIdSubmit}
            disabled={effectiveReadOnly || !clientIdInput.trim() || Boolean(discoveryInfo?.needs_client_secret && !clientSecretInput.trim())}
            className="px-4 py-2 text-sm font-medium bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            Continue
          </button>
        </div>
      </div>
    </div>
  );

  // Optional API key for open servers. Same shell as the client-id dialog;
  // only the copy and the empty-input behaviour differ — connecting without a
  // key is a valid choice here, so the submit button is never disabled.
  const apiKeyDialog = dialogMode === 'api_key' && (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 max-w-md w-full mx-4">
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <Key className="w-5 h-5 text-blue-500" />
            <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
              Connect {serverName}
            </h3>
          </div>
          <button
            onClick={handleApiKeyCancel}
            className="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 rounded"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <p className="text-sm text-gray-600 dark:text-gray-400 mb-4">
          This server works without a key. Adding one may unlock additional tools. Any key entered here is shared across AgentWorks, including Work, workflows, chats, and schedules.
        </p>

        <div className="mb-4">
          <label htmlFor="api-key-input" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
            API key <span className="font-normal text-gray-400">(optional)</span>
          </label>
          <input
            id="api-key-input"
            type="password"
            value={apiKeyInput}
            onChange={(e) => setApiKeyInput(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleApiKeySubmit()}
            placeholder="Leave blank to connect anonymously"
            className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md text-sm bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            autoFocus
          />
        </div>

        <div className="flex justify-end gap-2">
          <button
            onClick={handleApiKeyCancel}
            className="px-4 py-2 text-sm text-gray-600 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200 rounded-md transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleApiKeySubmit}
            disabled={effectiveReadOnly}
            className="px-4 py-2 text-sm font-medium bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            Connect
          </button>
        </div>
      </div>
    </div>
  );

  if (variant === 'icon') {
    // Connected reads as a filled green check; hovering swaps it for the X that
    // disconnects, so the resting state stays as calm as the directory grid.
    return (
      <>
        {clientIdDialog}
        {apiKeyDialog}
        <button
          onClick={() => {
            if (isConnected) {
              if (connectionDriven) handleDisconnect();
              else handleLogout();
            } else if (connectionDriven) {
              handleConnectClick();
            } else {
              handleLogin();
            }
          }}
          disabled={effectiveReadOnly || loading}
          className={`group/btn flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border transition-colors disabled:opacity-50 ${
            isConnected
              ? 'border-red-500/30 bg-red-500/10 text-red-500 hover:border-red-500/50 hover:bg-red-500/20 hover:text-red-400'
              : 'border-gray-300 text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:border-gray-600 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-100'
          }`}
          title={effectiveReadOnly ? readOnlyTitle : isConnected ? `Disconnect ${serverName}` : `Connect ${serverName}`}
          aria-label={isConnected ? `Disconnect ${serverName}` : `Connect ${serverName}`}
        >
          {loading ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : isConnected ? (
            <Trash2 className="h-4 w-4" />
          ) : (
            <Plus className="h-4 w-4" />
          )}
        </button>
      </>
    );
  }

  if (!isConnected) {
    return (
      <>
        {clientIdDialog}
        {apiKeyDialog}
        <div className="flex items-center gap-1">
          <button
            onClick={() => (connectionDriven ? handleConnectClick() : handleLogin())}
            disabled={effectiveReadOnly || loading}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-gray-100 text-white dark:text-gray-900 hover:bg-gray-700 dark:hover:bg-gray-300 rounded-md transition-colors disabled:opacity-50"
            title={effectiveReadOnly ? readOnlyTitle : 'Connect this shared platform service'}
          >
            {loading ? (
              <>
                <Loader2 className="w-3 h-3 animate-spin" />
                <span>Connecting...</span>
              </>
            ) : (
              <span>Connect</span>
            )}
          </button>
        </div>
      </>
    );
  }

  return (
    <div className="flex items-center gap-1.5">
      <button
        onClick={() => (connectionDriven ? handleDisconnect() : handleLogout())}
        disabled={effectiveReadOnly || loading}
        className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-300 border border-gray-300 dark:border-gray-600 hover:bg-red-50 dark:hover:bg-red-900/20 hover:text-red-600 dark:hover:text-red-400 hover:border-red-300 dark:hover:border-red-800 rounded-md transition-colors disabled:opacity-50"
        title={effectiveReadOnly ? readOnlyTitle : 'Disconnect this shared platform service for every user'}
        aria-label={`Disconnect ${serverName}`}
      >
        {loading ? (
          <>
            <Loader2 className="w-3 h-3 animate-spin" />
            <span>Disconnecting...</span>
          </>
        ) : (
          <span>Disconnect</span>
        )}
      </button>
    </div>
  );
};

export default OAuthStatusBadge;

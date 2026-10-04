import axios from 'axios';
import { getApiBaseUrl, getAuthToken } from '../services/api';

const API_BASE_URL = getApiBaseUrl();

const api = axios.create({
  baseURL: API_BASE_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Add auth token interceptor
api.interceptors.request.use((config) => {
  const authToken = getAuthToken()
  if (authToken && config.headers) {
    config.headers['Authorization'] = `Bearer ${authToken}`
  }
  return config
})

/**
 * Coding-CLI providers that support a per-workflow credential. Each scopes a
 * workflow to one person's own subscription instead of whichever login happens
 * to be on the server.
 */
export type WorkflowCredentialProvider = 'claude-code' | 'cursor-cli' | 'pi-cli';

export interface WorkflowProviderCredentialStatus {
  configured: boolean;
  updated_at?: string;
  /** Masked "first4...last4" rendering. The full value never leaves the server. */
  preview?: string;
}

const workflowProviderCredentialUrl = (provider: WorkflowCredentialProvider) =>
  `/api/workflow-provider-credentials/${provider}`;

export const secretsApi = {
  setVaultSecretAccess: async (groupId: string, name: string, allowed: boolean): Promise<void> => {
    await api.post('/api/secrets/vault/access', { group_id: groupId, name, allowed });
  },
  getVaultShareGroups: async (): Promise<{ groups: { ID: string; Name: string; Description?: string }[] }> => {
    const response = await api.get('/api/secrets/vault/share');
    return response.data;
  },
  shareWorkflowSecret: async (workspacePath: string, name: string, vaultName: string, groupIds: string[]): Promise<void> => {
    await api.post('/api/secrets/vault/share', { workspace_path: workspacePath, name, vault_name: vaultName, group_ids: groupIds });
  },
  promoteWorkflowSecret: async (workspacePath: string, name: string): Promise<void> => {
    await api.post('/api/secrets/global', { workspace_path: workspacePath, name });
  },
  saveGlobalSecret: async (name: string, value: string): Promise<void> => {
    const { encrypted } = await secretsApi.encrypt(value);
    await api.put('/api/secrets/global', { name, encrypted_value: encrypted });
  },
  deleteGlobalSecret: async (name: string): Promise<void> => {
    await api.delete('/api/secrets/global', { params: { name } });
  },
  revealGlobalSecret: async (name: string): Promise<{ value: string }> => {
    const response = await api.get('/api/secrets/global/reveal', { params: { name } });
    return response.data;
  },
  encrypt: async (value: string): Promise<{ encrypted: string }> => {
    const response = await api.post('/api/secrets/encrypt', { value });
    return response.data;
  },

  // workspacePath marks a shared workflow secret (bound to the workflow rather
  // than the caller); the server treats that as a reveal and allows it for the
  // workflow's owners only.
  decrypt: async (encrypted: string, workspacePath?: string): Promise<{ value: string }> => {
    const response = await api.post(
      '/api/secrets/decrypt',
      workspacePath ? { encrypted, workspace_path: workspacePath } : { encrypted },
    );
    return response.data;
  },

  getGlobalSecrets: async (manage = false): Promise<{ name: string; managed?: boolean }[]> => {
    const response = await api.get('/api/secrets/global', { params: manage ? { manage: true } : {} });
    return response.data;
  },

  storeWorkflowSecret: async (workspacePath: string, name: string, encryptedValue: string): Promise<{ success: boolean }> => {
    const response = await api.put('/api/secrets/workflow/store', {
      workspace_path: workspacePath,
      name,
      encrypted_value: encryptedValue,
    });
    return response.data;
  },

  deleteWorkflowSecret: async (workspacePath: string, name: string): Promise<{ success: boolean }> => {
    const response = await api.delete(`/api/secrets/workflow/store/${encodeURIComponent(name)}`, {
      params: { workspace_path: workspacePath },
    });
    return response.data;
  },

  listWorkflowSecrets: async (workspacePath: string): Promise<{ name: string; encrypted_value?: string }[]> => {
    const response = await api.get('/api/secrets/workflow/stored', {
      params: { workspace_path: workspacePath },
    });
    return response.data;
  },

  getWorkflowProviderCredentialStatus: async (provider: WorkflowCredentialProvider, workspacePath: string): Promise<WorkflowProviderCredentialStatus> => {
    const response = await api.get(workflowProviderCredentialUrl(provider), {
      params: { workspace_path: workspacePath },
    });
    return response.data;
  },

  storeWorkflowProviderCredential: async (provider: WorkflowCredentialProvider, workspacePath: string, token: string): Promise<{ success: boolean }> => {
    const encrypted = await secretsApi.encrypt(token);
    // The server validates the credential against the provider's CLI before
    // storing it, which is a real round trip — hence the extended timeout.
    const response = await api.put(workflowProviderCredentialUrl(provider), {
      workspace_path: workspacePath,
      encrypted_value: encrypted.encrypted,
    }, { timeout: 120_000 });
    return response.data;
  },

  deleteWorkflowProviderCredential: async (provider: WorkflowCredentialProvider, workspacePath: string): Promise<{ success: boolean }> => {
    const response = await api.delete(workflowProviderCredentialUrl(provider), {
      params: { workspace_path: workspacePath },
    });
    return response.data;
  },

  getWorkflowClaudeCodeCredentialStatus: (workspacePath: string) =>
    secretsApi.getWorkflowProviderCredentialStatus('claude-code', workspacePath),

  storeWorkflowClaudeCodeCredential: (workspacePath: string, token: string) =>
    secretsApi.storeWorkflowProviderCredential('claude-code', workspacePath, token),

  deleteWorkflowClaudeCodeCredential: (workspacePath: string) =>
    secretsApi.deleteWorkflowProviderCredential('claude-code', workspacePath),

  getWorkflowCursorCredentialStatus: (workspacePath: string) =>
    secretsApi.getWorkflowProviderCredentialStatus('cursor-cli', workspacePath),

  storeWorkflowCursorCredential: (workspacePath: string, apiKey: string) =>
    secretsApi.storeWorkflowProviderCredential('cursor-cli', workspacePath, apiKey),

  deleteWorkflowCursorCredential: (workspacePath: string) =>
    secretsApi.deleteWorkflowProviderCredential('cursor-cli', workspacePath),

  getWorkflowPiCLICredentialStatus: (workspacePath: string) =>
    secretsApi.getWorkflowProviderCredentialStatus('pi-cli', workspacePath),

  storeWorkflowPiCLICredential: (workspacePath: string, apiKey: string) =>
    secretsApi.storeWorkflowProviderCredential('pi-cli', workspacePath, apiKey),

  deleteWorkflowPiCLICredential: (workspacePath: string) =>
    secretsApi.deleteWorkflowProviderCredential('pi-cli', workspacePath),
};

export default secretsApi;

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

export interface SecretSources {
  status: 'available' | 'partial' | 'unavailable';
  note: string;
  warnings: string[];
  secrets: { name: string; display_name: string; explanation: string }[];
}

export const secretsApi = {
  getSecretSources: async (workspacePath: string): Promise<SecretSources> => {
    const response = await api.get('/api/secrets/workflow/sources', { params: { workspace_path: workspacePath } });
    return response.data;
  },
  setVaultSecretAccess: async (groupId: string, name: string, allowed: boolean): Promise<void> => {
    await api.post('/api/secrets/vault/access', { group_id: groupId, name, allowed });
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

  decrypt: async (encrypted: string): Promise<{ value: string }> => {
    const response = await api.post('/api/secrets/decrypt', { encrypted });
    return response.data;
  },

  getGlobalSecrets: async (manage = false): Promise<{ name: string; managed?: boolean }[]> => {
    const response = await api.get('/api/secrets/global', { params: manage ? { manage: true } : {} });
    return response.data;
  },

  // Saves a project's secret in its owner's Vault (My accounts) and uses it in the project.
  storeWorkflowSecret: async (workspacePath: string, name: string, encryptedValue: string): Promise<{ success: boolean }> => {
    const response = await api.put('/api/secrets/workflow/store', {
      workspace_path: workspacePath,
      name,
      encrypted_value: encryptedValue,
    });
    return response.data;
  },

  // Stops a project using a secret; its value stays in the owner's Vault.
  deleteWorkflowSecret: async (workspacePath: string, name: string): Promise<{ success: boolean }> => {
    const response = await api.delete(`/api/secrets/workflow/store/${encodeURIComponent(name)}`, {
      params: { workspace_path: workspacePath },
    });
    return response.data;
  },
};

export default secretsApi;

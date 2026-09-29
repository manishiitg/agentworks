import axios from 'axios';
import { useAuthStore } from '../../stores/useAuthStore';
import React, { useEffect, useMemo, useState } from 'react';
import { Badge } from '../ui/badge';
import { Button } from '../ui/Button';
import { Checkbox } from '../ui/checkbox';
import { SettingsCard } from '../ui/SettingsCard';
import { Input } from '../ui/Input';
import { KeyRound, Globe, Plus, Trash2, Eye, EyeOff } from 'lucide-react';
import ConfirmationDialog from '../ui/ConfirmationDialog';
import { useSecretsStore } from '../../stores';
import { secretsApi } from '../../api/secrets';
import { useCanWriteWorkflow, READ_ONLY_TITLE } from '../../hooks/useCanWriteWorkflow';
import { PROJECT_SECRETS_REFRESH_EVENT } from '../../utils/secretMutationRefresh';

/**
 * Where the secrets live when they are not a workflow's own: a Code passes
 * the signed-in person's personal secrets (products/work/personalSecretStore.ts),
 * so both use this one secrets UI. Every stored secret is in use; values are
 * write-only (no reveal, no promotion to global).
 */
export interface SecretStore {
  list: () => Promise<string[]>;
  save: (name: string, value: string) => Promise<void>;
  remove: (name: string) => Promise<void>;
  description?: React.ReactNode;
}

interface SecretSelectionSectionProps {
  selectedSecrets: string[];
  onSecretChange: (secrets: string[]) => void;
  selectedGlobalSecrets?: string[] | null; // null = all selected, [] = none selected
  onGlobalSecretChange?: (names: string[] | null) => void;
  workflowPath?: string;
  /** Lets the selector use an embedded side panel's remaining vertical space. */
  fillAvailableHeight?: boolean;
  workspaceNoun?: string;
  workspaceSecretHeading?: string;
  showGlobalSecrets?: boolean;
  workspaceSecretsAlwaysEnabled?: boolean;
  allowGlobalPromotion?: boolean;
  persistExplicitGlobalSelection?: boolean;
  store?: SecretStore;
}


const isValidName = (name: string) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(name);

export const SecretSelectionSection: React.FC<SecretSelectionSectionProps> = ({
  selectedSecrets,
  onSecretChange,
  selectedGlobalSecrets = [],
  onGlobalSecretChange,
  workflowPath,
  fillAvailableHeight = false,
  workspaceNoun = 'workflow',
  workspaceSecretHeading = 'Automation Secrets',
  showGlobalSecrets = true,
  workspaceSecretsAlwaysEnabled = false,
  allowGlobalPromotion = true,
  persistExplicitGlobalSelection = false,
  store,
}) => {
  const globalSecrets = useSecretsStore((s) => s.globalSecrets);
  const workflowSecretsByPath = useSecretsStore((s) => s.workflowSecretsByPath);
  const fetchGlobalSecrets = useSecretsStore((s) => s.fetchGlobalSecrets);
  const fetchWorkflowSecrets = useSecretsStore((s) => s.fetchWorkflowSecrets);
  const addWorkflowSecret = useSecretsStore((s) => s.addWorkflowSecret);
  const removeWorkflowSecret = useSecretsStore((s) => s.removeWorkflowSecret);
  // Workflow secrets are shared by everyone with access to the workflow:
  // owners add, delete and reveal them; a read-only user sees the names and
  // may run the workflow with them, but the server refuses reveal and every
  // mutation, so the controls disable here rather than fail on click.
  const canWriteWorkflow = useCanWriteWorkflow(workflowPath?.trim() || undefined);
  // A person's own store: they always manage it.
  const canWrite = store ? true : canWriteWorkflow;
  const isAdmin = useAuthStore(state => state.user?.is_admin === true || (state.isMultiUserModeChecked && !state.isMultiUserMode));
  const [globalBusy, setGlobalBusy] = useState(false);
  const [globalStatus, setGlobalStatus] = useState('');
  const manageGlobal = async (action: 'promote' | 'delete', name: string) => {
    if (!isAdmin || globalBusy) return;
    setGlobalBusy(true); setGlobalStatus('');
    try {
      if (action === 'promote') await secretsApi.promoteWorkflowSecret(workflowPath!.trim(), name);
      else await secretsApi.deleteGlobalSecret(name);
      await fetchGlobalSecrets();
      if (workflowPath) await fetchWorkflowSecrets(workflowPath.trim());
      setGlobalStatus(action === 'delete' ? `Removed global secret ${name}.` : `${name} is global. Changes apply to new turns and runs.`);
    } catch (error) {
      setGlobalStatus(axios.isAxiosError(error) && typeof error.response?.data === 'string' ? error.response.data : 'Could not update global secret.');
    } finally { setGlobalBusy(false); }
  };

  const [workflowSecretName, setWorkflowSecretName] = useState('');
  const [workflowSecretValue, setWorkflowSecretValue] = useState('');
  const [workflowSecretError, setWorkflowSecretError] = useState<string | null>(null);
  const [savingWorkflowSecret, setSavingWorkflowSecret] = useState(false);
  // Revealed automation-secret values, decrypted on demand and dropped again
  // on hide so plaintext never sits in state longer than it is on screen.
  const [revealedValues, setRevealedValues] = useState<Record<string, string>>({});
  const [revealingName, setRevealingName] = useState<string | null>(null);

  const toggleReveal = async (secret: { name: string; encrypted_value?: string }) => {
    if (revealedValues[secret.name] !== undefined) {
      setRevealedValues((current) => {
        const next = { ...current };
        delete next[secret.name];
        return next;
      });
      return;
    }
    if (!secret.encrypted_value) return;
    setRevealingName(secret.name);
    try {
      const { value } = await secretsApi.decrypt(secret.encrypted_value, workflowPath?.trim() || undefined);
      setRevealedValues((current) => ({ ...current, [secret.name]: value }));
    } catch {
      setWorkflowSecretError(`Could not read the value of ${secret.name}.`);
    } finally {
      setRevealingName(null);
    }
  };

  // Global reveal mirrors the scoped one: admin-only, decrypted on demand
  // through /api/secrets/global/reveal, dropped from state on hide.
  const [revealedGlobals, setRevealedGlobals] = useState<Record<string, string>>({});
  const [revealingGlobalName, setRevealingGlobalName] = useState<string | null>(null);

  const toggleRevealGlobal = async (name: string) => {
    if (revealedGlobals[name] !== undefined) {
      setRevealedGlobals((current) => {
        const next = { ...current };
        delete next[name];
        return next;
      });
      return;
    }
    setRevealingGlobalName(name);
    try {
      const { value } = await secretsApi.revealGlobalSecret(name);
      setRevealedGlobals((current) => ({ ...current, [name]: value }));
    } catch {
      setGlobalStatus(`Could not read the value of ${name}.`);
    } finally {
      setRevealingGlobalName(null);
    }
  };

  const normalizedWorkflowPath = workflowPath?.trim() || '';
  const [storeNames, setStoreNames] = useState<string[]>([]);
  const refreshStore = React.useCallback(async () => {
    if (!store) return;
    try {
      setStoreNames(await store.list());
    } catch {
      setWorkflowSecretError('Could not load your secrets.');
    }
  }, [store]);
  useEffect(() => { void refreshStore(); }, [refreshStore]);
  const workflowSecrets: Array<{ name: string; encrypted_value?: string }> = store
    ? storeNames.map(name => ({ name }))
    : normalizedWorkflowPath
      ? workflowSecretsByPath[normalizedWorkflowPath] || []
      : [];
  const hasSecretCard = !!normalizedWorkflowPath || !!store;
  const secretsAlwaysEnabled = workspaceSecretsAlwaysEnabled || !!store;
  const canPromote = allowGlobalPromotion && !store;

  useEffect(() => {
    if (globalSecrets.length === 0) {
      fetchGlobalSecrets();
    }
  }, [fetchGlobalSecrets, globalSecrets.length]);

  useEffect(() => {
    if (normalizedWorkflowPath && !store) {
      fetchWorkflowSecrets(normalizedWorkflowPath);
    }
  }, [normalizedWorkflowPath, fetchWorkflowSecrets, store]);

  // Secrets created by the agent use the server-side project tools, so they
  // do not pass through this component's local add action. Refresh the visible
  // list as soon as that tool completes instead of requiring a page reload.
  useEffect(() => {
    if (!normalizedWorkflowPath) return
    const refresh = () => { void fetchWorkflowSecrets(normalizedWorkflowPath) }
    window.addEventListener(PROJECT_SECRETS_REFRESH_EVENT, refresh)
    return () => window.removeEventListener(PROJECT_SECRETS_REFRESH_EVENT, refresh)
  }, [normalizedWorkflowPath, fetchWorkflowSecrets])

  const toggleSecretName = (name: string) => {
    if (selectedSecrets.includes(name)) {
      onSecretChange(selectedSecrets.filter(s => s !== name));
    } else {
      onSecretChange([...selectedSecrets, name]);
    }
  };

  const selectedSecretNames = useMemo(() => new Set(selectedSecrets), [selectedSecrets]);

  const toggleGlobal = (name: string) => {
    if (!onGlobalSecretChange) return;
    const attachedByName = selectedSecretNames.has(name);
    if (attachedByName) onSecretChange(selectedSecrets.filter(selected => selected !== name));
    const isSelected = selectedGlobalSecrets === null || selectedGlobalSecrets.includes(name);
    if (isSelected) {
      const remaining = (selectedGlobalSecrets ?? globalSecrets.map(g => g.name)).filter(n => n !== name);
      onGlobalSecretChange(remaining);
    } else if (!attachedByName) {
      const next = [...(selectedGlobalSecrets ?? []), name];
      onGlobalSecretChange(!persistExplicitGlobalSelection && next.length === globalSecrets.length ? null : next);
    }
  };

  const handleSaveWorkflowSecret = async () => {
    if (!hasSecretCard) return;
    setWorkflowSecretError(null);
    const trimmedName = workflowSecretName.trim().toUpperCase();
    if (!trimmedName) {
      setWorkflowSecretError('Name is required');
      return;
    }
    if (!isValidName(trimmedName)) {
      setWorkflowSecretError('Name must start with a letter or underscore and contain only letters, numbers, and underscores');
      return;
    }
    if (!workflowSecretValue) {
      setWorkflowSecretError('Value is required');
      return;
    }

    setSavingWorkflowSecret(true);
    if (store) {
      try {
        await store.save(trimmedName, workflowSecretValue);
        setWorkflowSecretName('');
        setWorkflowSecretValue('');
        await refreshStore();
      } catch (err) {
        const data = axios.isAxiosError(err) ? (err.response?.data as { error?: string } | undefined) : undefined;
        setWorkflowSecretError(data?.error || (err instanceof Error ? err.message : 'Failed to save secret'));
      } finally {
        setSavingWorkflowSecret(false);
      }
      return;
    }
    try {
      const { encrypted } = await secretsApi.encrypt(workflowSecretValue);
      await addWorkflowSecret(normalizedWorkflowPath, trimmedName, encrypted);
      if (!selectedSecretNames.has(trimmedName)) {
        onSecretChange([...selectedSecrets, trimmedName]);
      }
      setWorkflowSecretName('');
      setWorkflowSecretValue('');
    } catch (err) {
      setWorkflowSecretError(err instanceof Error ? err.message : `Failed to save ${workspaceNoun} secret`);
    } finally {
      setSavingWorkflowSecret(false);
    }
  };

  const handleDeleteWorkflowSecret = async (name: string) => {
    if (store) {
      try {
        await store.remove(name);
      } catch {
        setWorkflowSecretError(`Could not delete ${name}.`);
      }
      await refreshStore();
      return;
    }
    if (!normalizedWorkflowPath) return;
    await removeWorkflowSecret(normalizedWorkflowPath, name);
    onSecretChange(selectedSecrets.filter(s => s !== name));
  };

  // Destructive and scope-widening actions confirm through the shared dialog
  // instead of a native alert. The dialog stays open with a spinner while a
  // global mutation runs; a scoped delete closes first and runs behind it.
  const [pendingConfirm, setPendingConfirm] = useState<null | { kind: 'promote' | 'delete-global' | 'delete-scoped'; name: string }>(null);
  const runPendingConfirm = () => {
    if (!pendingConfirm) return;
    if (pendingConfirm.kind === 'delete-scoped') {
      const name = pendingConfirm.name;
      setPendingConfirm(null);
      void handleDeleteWorkflowSecret(name);
      return;
    }
    const action = pendingConfirm.kind === 'promote' ? 'promote' : 'delete';
    void manageGlobal(action, pendingConfirm.name).finally(() => setPendingConfirm(null));
  };
  const confirmCopy = pendingConfirm === null ? null : {
    promote: {
      title: `Make ${pendingConfirm.name} global?`,
      message: 'It will be available server-wide to all users and workflows. Its source workflow will use the global value.',
      confirmText: 'Make global',
      loadingText: 'Making global...',
      type: 'warning' as const,
    },
    'delete-global': {
      title: `Delete global secret ${pendingConfirm.name}?`,
      message: 'Workflows using it will no longer receive its value on new runs.',
      confirmText: 'Delete',
      loadingText: 'Deleting...',
      type: 'danger' as const,
    },
    'delete-scoped': {
      title: `Delete ${workspaceNoun} secret "${pendingConfirm.name}"?`,
      message: 'The stored value is removed. Anything attaching this name stops receiving it.',
      confirmText: 'Delete',
      loadingText: 'Deleting...',
      type: 'danger' as const,
    },
  }[pendingConfirm.kind];

  if (globalSecrets.length === 0 && workflowSecrets.length === 0 && !hasSecretCard) return null;

  const sortedWorkflowSecrets = [...workflowSecrets].sort((a, b) => a.name.localeCompare(b.name));
  const hasRows = sortedWorkflowSecrets.length > 0
    || (showGlobalSecrets && globalSecrets.length > 0);

  return (
    <div className={fillAvailableHeight ? 'flex h-full min-h-0 flex-col gap-2' : 'space-y-4'}>
      {hasSecretCard && canWrite && (
        <SettingsCard
          icon={<KeyRound aria-hidden="true" className="h-4 w-4 text-primary" />}
          title={workspaceSecretHeading}
          count={`${sortedWorkflowSecrets.length} saved`}
          description={store?.description ?? <span className="block truncate">{normalizedWorkflowPath}</span>}
          className="shrink-0"
        >
          <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto]">
            <Input
              type="text"
              value={workflowSecretName}
              onChange={(e) => setWorkflowSecretName(e.target.value.toUpperCase())}
              placeholder="SECRET_NAME"
              aria-label="Secret name"
              className="min-w-0"
            />
            <Input
              type="password"
              value={workflowSecretValue}
              onChange={(e) => setWorkflowSecretValue(e.target.value)}
              placeholder="Secret value"
              aria-label="Secret value"
              className="min-w-0"
            />
            <Button
              type="button"
              onClick={handleSaveWorkflowSecret}
              disabled={savingWorkflowSecret}
            >
              <Plus className="h-4 w-4" />
              {savingWorkflowSecret ? 'Saving' : 'Save'}
            </Button>
          </div>
          {workflowSecretError && <p className="text-xs text-destructive">{workflowSecretError}</p>}
        </SettingsCard>
      )}

      {globalStatus && <p role="status" className="text-xs text-muted-foreground">{globalStatus}</p>}

      {hasRows && <div className={`rounded-md border border-border bg-card ${fillAvailableHeight ? 'min-h-0 flex-1 overflow-y-auto' : ''}`}>
        {sortedWorkflowSecrets.map((secret) => (
          <div key={`workflow-${secret.name}`} className="flex items-center gap-2 border-b border-border p-3 last:border-b-0 hover:bg-muted">
            <Checkbox
              id={`workflow-secret-${secret.name}`}
              checked={secretsAlwaysEnabled || selectedSecretNames.has(secret.name)}
              onCheckedChange={() => toggleSecretName(secret.name)}
              disabled={secretsAlwaysEnabled}
            />
            <label htmlFor={`workflow-secret-${secret.name}`} className="flex min-w-0 flex-1 cursor-pointer select-none items-center gap-2 text-sm text-foreground">
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="min-w-0 truncate font-mono">{secret.name}</span>
                {revealedValues[secret.name] !== undefined && (
                  <span className="mt-0.5 break-all font-mono text-xs text-muted-foreground">{revealedValues[secret.name]}</span>
                )}
              </span>
            </label>
            {canPromote && isAdmin && canWrite && <Button type="button" variant="link" size="sm" disabled={globalBusy} onClick={() => setPendingConfirm({ kind: 'promote', name: secret.name })} className="shrink-0" aria-label={`Make ${secret.name} global`}>Make global</Button>}
            {!store && <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => { void toggleReveal(secret) }}
              disabled={!canWrite || !secret.encrypted_value || revealingName === secret.name}
              className="h-7 w-7 shrink-0"
              title={!canWrite ? READ_ONLY_TITLE : revealedValues[secret.name] !== undefined ? 'Hide value' : secret.encrypted_value ? 'Show value' : 'Value not available'}
            >
              {revealedValues[secret.name] !== undefined ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
            </Button>}
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => setPendingConfirm({ kind: 'delete-scoped', name: secret.name })}
              disabled={!canWrite}
              className="h-7 w-7 shrink-0 hover:text-destructive"
              title={canWrite ? `Delete ${workspaceNoun} secret` : READ_ONLY_TITLE}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        ))}

        {showGlobalSecrets && globalSecrets.map((gs) => (
          <div key={`global-${gs.name}`} className="flex items-center gap-2 border-b border-border p-3 last:border-b-0 hover:bg-muted">
            <Checkbox
              id={`global-secret-${gs.name}`}
              checked={selectedSecretNames.has(gs.name) || selectedGlobalSecrets === null || selectedGlobalSecrets.includes(gs.name)}
              onCheckedChange={() => toggleGlobal(gs.name)}
              disabled={!onGlobalSecretChange}
            />
            <label htmlFor={`global-secret-${gs.name}`} className="flex min-w-0 flex-1 cursor-pointer select-none items-center gap-2 text-sm text-foreground">
              <Globe className="h-3.5 w-3.5 flex-shrink-0 text-primary" />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="min-w-0 truncate font-mono">{gs.name}</span>
                {revealedGlobals[gs.name] !== undefined && (
                  <span className="mt-0.5 break-all font-mono text-xs text-muted-foreground">{revealedGlobals[gs.name]}</span>
                )}
              </span>
              <Badge className="ml-auto shrink-0">Global</Badge>
            </label>
            {isAdmin && (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => { void toggleRevealGlobal(gs.name) }}
                disabled={revealingGlobalName === gs.name}
                className="h-7 w-7 shrink-0"
                title={revealedGlobals[gs.name] !== undefined ? 'Hide value' : 'Show value'}
                aria-label={`Reveal global ${gs.name}`}
              >
                {revealedGlobals[gs.name] !== undefined ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
              </Button>
            )}
            {isAdmin && gs.managed && (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => setPendingConfirm({ kind: 'delete-global', name: gs.name })}
                disabled={globalBusy}
                className="h-7 w-7 shrink-0 hover:text-destructive"
                title="Delete global secret"
                aria-label={`Delete global ${gs.name}`}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        ))}

      </div>}

      {confirmCopy && <ConfirmationDialog
        isOpen={pendingConfirm !== null}
        onClose={() => setPendingConfirm(null)}
        onConfirm={runPendingConfirm}
        title={confirmCopy.title}
        message={confirmCopy.message}
        confirmText={confirmCopy.confirmText}
        type={confirmCopy.type}
        isLoading={globalBusy}
        loadingText={confirmCopy.loadingText}
      />}
    </div>
  );
};

export default SecretSelectionSection;

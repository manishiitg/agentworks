package server

import (
	"context"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/sealbox"
)

// Workflow secrets belong to the workflow, not to whichever user typed them
// in. Before PLAT-272 they lived under the storing user's own
// _users/<uid>/workflow_secrets/ document and were AES-GCM bound to that
// user's ID, so a second user with access to the workflow -- a read-only
// reviewer running it, a co-owner, or a scheduled run started as anyone else
// -- resolved nothing and every $SECRET_<NAME> came up empty. This file is the
// shared store that replaces that: one document per workflow under the
// reserved chathistory.SharedWorkflowSecretsUserID, bound to the workflow
// path instead of a user, gated by workflow access (owners manage and reveal,
// readers may use at runtime but never see values).

// sharedWorkflowSecretAAD is the AES-GCM additional data for a shared
// workflow secret: the store's canonical workflow path. Binding to the path
// means a ciphertext lifted from one workflow's document cannot be replayed
// into another's.
func sharedWorkflowSecretAAD(workflowPath string) ([]byte, error) {
	normalized, err := chathistory.NormalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		return nil, err
	}
	return []byte("workflow:" + normalized), nil
}

// encryptSecretValueWithAAD seals plaintext with the server secrets key and
// the given additional data; the result is base64(nonce || ciphertext), the
// same wire shape /api/secrets/encrypt produces. (pkg/sealbox is the one
// AES-GCM helper; secrets_routes.go, provider_keys_store.go and
// services/workspace_config.go still carry their own copies to migrate.)
func encryptSecretValueWithAAD(plaintext string, aad []byte) (string, error) {
	return sealbox.Seal(deriveSecretsKey(), plaintext, aad)
}

// decryptSecretValueWithAAD is the inverse of encryptSecretValueWithAAD.
func decryptSecretValueWithAAD(encryptedBase64 string, aad []byte) (string, error) {
	return sealbox.Open(deriveSecretsKey(), encryptedBase64, aad)
}

// upsertSharedWorkflowSecret stores plaintext for everyone with access to the
// workflow.
func (api *StreamingAPI) upsertSharedWorkflowSecret(ctx context.Context, workflowPath, name, plaintext string) error {
	aad, err := sharedWorkflowSecretAAD(workflowPath)
	if err != nil {
		return err
	}
	encrypted, err := encryptSecretValueWithAAD(plaintext, aad)
	if err != nil {
		return err
	}
	if err := api.chatStore.UpsertWorkflowSecret(ctx, chathistory.SharedWorkflowSecretsUserID, workflowPath, name, encrypted); err != nil {
		return err
	}
	markWorkflowSecretsChanged(workflowPath)
	return nil
}

// deleteSharedWorkflowSecret removes a shared workflow secret. The caller's
// own legacy per-user copy, if any survived migration, is removed too so the
// name cannot resurface through a later migration pass.
func (api *StreamingAPI) deleteSharedWorkflowSecret(ctx context.Context, workflowPath, name, callerUserID string) error {
	if err := api.chatStore.DeleteWorkflowSecret(ctx, chathistory.SharedWorkflowSecretsUserID, workflowPath, name); err != nil {
		return err
	}
	markWorkflowSecretsChanged(workflowPath)
	if callerUserID != "" && callerUserID != chathistory.SharedWorkflowSecretsUserID {
		if err := api.chatStore.DeleteWorkflowSecret(ctx, callerUserID, workflowPath, name); err != nil {
			log.Printf("[SECRETS] shared delete of %q for %s succeeded but the legacy per-user copy for %s could not be removed: %v", name, workflowPath, callerUserID, err)
		}
	}
	return nil
}

// decryptSharedWorkflowSecret returns the plaintext of one shared secret
// record. Access is the caller's responsibility.
func decryptSharedWorkflowSecret(workflowPath string, s chathistory.UserSecret) (string, error) {
	aad, err := sharedWorkflowSecretAAD(workflowPath)
	if err != nil {
		return "", err
	}
	return decryptSecretValueWithAAD(s.EncryptedValue, aad)
}

// ensureSharedWorkflowSecrets lists the workflow's shared secrets, migrating
// the pre-PLAT-272 per-user documents into the shared one first if the shared
// document is still empty. Migration candidates are the requesting user, the
// workflow's creator, and its listed owners -- the identities that could have
// stored a value through the old per-user path. Migrated entries are removed
// from the per-user documents so this is a one-shot move, not a lingering
// second source; a legacy entry that fails to decrypt (a different
// AUTH_SECRET, a corrupt blob) is left where it is and logged.
func (api *StreamingAPI) ensureSharedWorkflowSecrets(ctx context.Context, workflowPath, requestingUserID string) ([]chathistory.UserSecret, error) {
	if strings.TrimSpace(workflowPath) == "" {
		return nil, nil
	}
	shared, err := api.chatStore.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, workflowPath)
	if err != nil {
		return nil, err
	}
	if len(shared) > 0 {
		return shared, nil
	}

	candidates := []string{requestingUserID}
	if manifest, exists, err := ReadWorkflowManifest(ctx, workflowPath); err == nil && exists && manifest != nil {
		candidates = append(candidates, manifest.CreatedBy)
		candidates = append(candidates, manifest.effectiveOwners()...)
	}
	seen := map[string]bool{"": true, chathistory.SharedWorkflowSecretsUserID: true}
	migrated := 0
	for _, uid := range candidates {
		uid = strings.TrimSpace(uid)
		if seen[uid] {
			continue
		}
		seen[uid] = true
		legacy, err := api.chatStore.ListWorkflowSecrets(ctx, uid, workflowPath)
		if err != nil {
			log.Printf("[SECRETS] migration: cannot read legacy workflow secrets of %s for %s: %v", uid, workflowPath, err)
			continue
		}
		for _, s := range legacy {
			plaintext, err := decryptSecretValue(s.EncryptedValue, uid)
			if err != nil {
				log.Printf("[SECRETS] migration: legacy secret %q of %s for %s left in place, cannot decrypt: %v", s.Name, uid, workflowPath, err)
				continue
			}
			if err := api.upsertSharedWorkflowSecret(ctx, workflowPath, s.Name, plaintext); err != nil {
				log.Printf("[SECRETS] migration: cannot write shared secret %q for %s: %v", s.Name, workflowPath, err)
				continue
			}
			if err := api.chatStore.DeleteWorkflowSecret(ctx, uid, workflowPath, s.Name); err != nil {
				log.Printf("[SECRETS] migration: shared copy of %q written for %s but the legacy copy of %s could not be removed: %v", s.Name, workflowPath, uid, err)
			}
			migrated++
		}
	}
	if migrated == 0 {
		return shared, nil
	}
	log.Printf("[SECRETS] migrated %d workflow secret(s) for %s from per-user storage into the shared workflow store", migrated, workflowPath)
	return api.chatStore.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, workflowPath)
}

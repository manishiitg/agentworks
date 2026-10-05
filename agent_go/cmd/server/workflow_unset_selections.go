package server

import (
	"context"
	"sort"
	"strings"
)

// missingSelectedSecrets lists the secret names a workflow selects that have no
// stored value in the workflow's own secrets or the global secrets. A run still
// starts without them ($SECRET_<NAME> is empty); the workflow shows a banner so
// the person can add the value or drop the selection.
func (api *StreamingAPI) missingSelectedSecrets(ctx context.Context, userID, workspacePath string, manifest *WorkflowManifest) []string {
	if manifest == nil {
		return nil
	}
	wanted := map[string]bool{}
	for _, name := range manifest.Capabilities.SelectedSecrets {
		if name = strings.TrimSpace(name); name != "" {
			wanted[name] = true
		}
	}
	if manifest.Capabilities.SelectedGlobalSecretNames != nil {
		for _, name := range *manifest.Capabilities.SelectedGlobalSecretNames {
			if name = strings.TrimSpace(name); name != "" {
				wanted[name] = true
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	if own, err := api.ensureSharedWorkflowSecrets(ctx, workspacePath, userID); err == nil {
		for _, s := range own {
			delete(wanted, s.Name)
		}
	}
	for _, s := range getGlobalSecrets() {
		delete(wanted, s.Name)
	}
	missing := make([]string, 0, len(wanted))
	for name := range wanted {
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return missing
}

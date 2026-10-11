package workflowkb

import (
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"os"
	"path/filepath"
)

// SharedConfig reads only a canonical manifest inside the installation.
func SharedConfig(root, workspace string) (bool, string) {
	if workspace == "" {
		return false, ""
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, ""
	}
	path, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(workspace), "workflow.json"))
	if os.IsNotExist(err) {
		return false, ""
	}
	if err != nil || !Within(base, path) {
		return true, "Knowledge configuration unavailable; legacy knowledge access is disabled."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, "Knowledge configuration unavailable; legacy knowledge access is disabled."
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return true, "Knowledge configuration is invalid; legacy knowledge access is disabled."
	}
	var m struct {
		Mode string `json:"knowledgebase_mode"`
	}
	if json.Unmarshal(data, &m) != nil {
		return true, "Knowledge configuration is invalid; legacy knowledge access is disabled."
	}
	if m.Mode != "" && m.Mode != "shared" {
		return true, "Knowledge mode is invalid; legacy knowledge access is disabled."
	}
	if m.Mode != "shared" {
		return false, ""
	}
	if !productpolicy.Enabled("knowledgebase") {
		return true, "## Shared knowledge unavailable\nThis project requires a shared knowledge product that is disabled in this installation. Its retired local knowledgebase is not a fallback. Project files and workflow-local learnings remain available under their existing permissions."
	}
	// Folder bindings were removed (PLAT-628): a migrated project's steps name the Brain folders they use.
	return true, "## Shared Knowledge Base\nThis workflow's knowledge lives in Brain; the local knowledgebase/ folder is retired. Use brain_browse and brain_read on the Brain folders and notes this step's description names, and brain_update only when this step may write. Read an entry before patching it; use its version and a unique request_id."
}

func LegacyKnowledgeBlocks(root, workspace string) []string {
	var blocked []string
	if shared, _ := SharedConfig(root, workspace); shared {
		blocked = append(blocked, filepath.Join(workspace, "knowledgebase"))
	}
	consumer, err := ReadManifest(root, workspace)
	if err != nil || len(consumer.Sources) == 0 {
		return blocked
	}
	registry, err := Discover(root)
	if err != nil {
		return blocked
	}
	for _, ref := range consumer.Sources {
		for _, source := range registry[ref.WorkflowID] {
			if shared, _ := SharedConfig(root, source); shared {
				blocked = append(blocked, filepath.Join(source, "knowledgebase"))
			}
		}
	}
	return blocked
}

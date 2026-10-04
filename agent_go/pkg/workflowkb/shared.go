package workflowkb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		Mode     string                           `json:"knowledgebase_mode"`
		Bindings []struct{ Alias, Access string } `json:"shared_knowledgebase"`
	}
	if json.Unmarshal(data, &m) != nil {
		return true, "Knowledge configuration is invalid; legacy knowledge access is disabled."
	}
	if m.Mode != "" && m.Mode != "shared" {
		return true, "Knowledge mode is invalid; legacy knowledge access is disabled."
	}
	lines := []string{"## Shared Knowledge Base", "Use browse_knowledgebase and read_knowledgebase with binding_alias. Contribute through update_knowledgebase only when the binding and this step allow writes. Read an entry before patching it; use its version and a unique request_id. Local knowledgebase/ is an archived migration source and cannot be read or edited after cutover. Bindings grant no permissions; unavailable access must be reported to the owner."}
	for _, b := range m.Bindings {
		lines = append(lines, fmt.Sprintf("- %s: %s", b.Alias, b.Access))
	}
	if len(m.Bindings) == 0 {
		return m.Mode == "shared", ""
	}
	return m.Mode == "shared", strings.Join(lines, "\n")
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

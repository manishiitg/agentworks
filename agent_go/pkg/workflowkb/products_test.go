package workflowkb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalProductSelectionPreservesKnowledgeAndRetiredArchive(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	root := t.TempDir()
	workspace := fixture(t, root, "local", "local-id", "default", nil)
	if shared, text := SharedConfig(root, workspace); shared || text != "" || len(LegacyKnowledgeBlocks(root, workspace)) != 0 {
		t.Fatal("ordinary local KB was disabled")
	}
	manifest := filepath.Join(root, workspace, "workflow.json")
	if err := os.WriteFile(manifest, []byte(`{"id":"local-id","knowledgebase_mode":"shared"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if shared, text := SharedConfig(root, workspace); !shared || strings.Contains(text, "brain_read") || !strings.Contains(text, "disabled") {
		t.Fatalf("disabled Brain guidance: %s", text)
	}
	if len(LegacyKnowledgeBlocks(root, workspace)) != 1 {
		t.Fatal("disabling Brain reopened retired archive")
	}
	if data, err := os.ReadFile(filepath.Join(root, workspace, "knowledgebase", "notes", "fact.md")); err != nil || string(data) != "local" {
		t.Fatal("local KB data changed")
	}
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	if shared, text := SharedConfig(root, workspace); !shared || !strings.Contains(text, "brain_read") {
		t.Fatal("opt-in did not restore shared knowledge guidance")
	}
}

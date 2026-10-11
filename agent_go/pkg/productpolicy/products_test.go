package productpolicy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

func profile(t *testing.T, mode, opt, products, surfaces string) {
	t.Helper()
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", mode)
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", opt)
	t.Setenv("AGENT_PRODUCTS", products)
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", surfaces)
}
func TestInstallationSelection(t *testing.T) {
	for _, tc := range []struct {
		name, mode, opt, products, surfaces string
		server                              bool
	}{
		{name: "local stale config", mode: "local", products: "agentworks,work,code,knowledgebase,mcp-gateway,llm-gateway,relays", server: false},
		{name: "local opt in", mode: "local", opt: "1", server: true},
		{name: "server single user", mode: "server", server: true},
		{name: "unknown mode retains shared server compatibility", server: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile(t, tc.mode, tc.opt, tc.products, tc.surfaces)
			for _, p := range []string{"code", "knowledgebase", "mcp-gateway", "llm-gateway", "relays", "caplayer", "brain"} {
				if Enabled(p) != tc.server {
					t.Errorf("Enabled(%s)=%t", p, Enabled(p))
				}
			}
			for _, p := range []string{"agentworks", "work"} {
				if !Enabled(p) {
					t.Errorf("core project %s unavailable", p)
				}
			}
		})
	}
	profile(t, "local", "1", "", "agentworks,work,code,mcp-gateway")
	if Enabled("knowledgebase") || Enabled("relays") || Enabled("llm-gateway") || !Enabled("mcp-gateway") {
		t.Fatal("explicit enabled surfaces must narrow opt-in")
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if Enabled("knowledgebase") || Enabled("relays") {
		t.Fatal("server ignores enabled surfaces")
	}
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENT_PRODUCTS", "sparkquill")
	if Enabled("knowledgebase") || Enabled("code") || !Enabled("sparkquill") {
		t.Fatal("dedicated deployment allowlist drifted")
	}
}
func TestCallerCannotEnableUninstalledProducts(t *testing.T) {
	profile(t, "local", "0", "", "")
	s := Selection{Allowed: func(string) bool { return true }}
	if s.Has("vault") || s.Has("code") {
		t.Fatal("caller bypassed local installation")
	}
	if s.AllowsBinding("code.create-project") || s.AllowsTool("create_code_workspace") || s.AllowsSkill("code-mcp") {
		t.Fatal("disabled Code capability remains available")
	}
	if !s.AllowsSkill("code-authoring") || !s.AllowsTool("execute_shell_command") {
		t.Fatal("ordinary CLI/script authoring was removed")
	}
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	s = Selection{Allowed: func(p string) bool { return p != "knowledgebase" }}
	if s.Has("brain") || !s.Has("vault") {
		t.Fatal("caller product restriction not applied")
	}
	if got := FromContext(WithSelection(context.Background(), s)); got.Has("knowledgebase") {
		t.Fatal("child lost selection")
	}
}
func TestTrustedTextAndSkillProjection(t *testing.T) {
	profile(t, "local", "0", "", "")
	s := Selection{}
	raw := "project secrets <!-- product:mcp-gateway -->Vault <!-- product:knowledgebase -->Brain<!-- /product --><!-- /product --> local learnings"
	if got := s.Text(raw); strings.Contains(got, "Vault") || strings.Contains(got, "Brain") || !strings.Contains(got, "local learnings") {
		t.Fatal(got)
	}
	if got := s.Text("safe <!-- product:unknown -->unfinished"); got != "safe " {
		t.Fatal(got)
	}
	rawSkill := &llmtypes.Skill{Name: "builder-reference", Content: raw, Source: llmtypes.SkillSource{Origin: "builtin"}, SupportingFiles: []llmtypes.SkillFile{{RelPath: "local.md", Content: []byte("keep")}, {RelPath: "shared.md", Content: []byte("<!-- product:mcp-gateway -->Vault<!-- /product -->")}}}
	got := s.Skill(rawSkill)
	if len(got.SupportingFiles) != 1 || strings.Contains(got.Content, "Vault") || rawSkill.Content != raw || len(rawSkill.SupportingFiles) != 2 {
		t.Fatal("projection mutated shared registry or retained unavailable guidance")
	}
	imported := *rawSkill
	imported.Name = "my-project-skill"
	imported.Source.Origin = "imported"
	if s.Skill(&imported) != &imported {
		t.Fatal("project-authored content changed")
	}
	if s.Skill(&llmtypes.Skill{Name: "brain"}) != nil {
		t.Fatal("stale explicit Brain selection was attached")
	}
	original := map[string]any{"properties": map[string]any{"source": map[string]any{"description": raw}}}
	filtered := s.Schema(original).(map[string]any)
	desc := filtered["properties"].(map[string]any)["source"].(map[string]any)["description"].(string)
	if strings.Contains(desc, "Vault") || original["properties"].(map[string]any)["source"].(map[string]any)["description"] != raw {
		t.Fatal("schema projection is not independent")
	}
}
func TestProjectionCleanupPreservesUserSkillsAndSymlinks(t *testing.T) {
	profile(t, "local", "0", "", "")
	root := t.TempDir()
	mkdir := func(name string, marked bool) string {
		p := filepath.Join(root, ".agents", "skills", name)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		if marked {
			if err := os.WriteFile(filepath.Join(p, projectfile.SkillMarkerFile), []byte("managed"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	disabled := mkdir("brain", true)
	disabledCode := mkdir("code-mcp", true)
	user := mkdir("vault-access", false)
	local := mkdir("workflow-learnings", true)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".agents", "skills", "relay-builder")); err != nil {
		t.Fatal(err)
	}
	if err := CleanupProjected(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(disabled); !os.IsNotExist(err) {
		t.Fatal("disabled projection remains")
	}
	if _, err := os.Stat(disabledCode); !os.IsNotExist(err) {
		t.Fatal("disabled Code projection remains")
	}
	for _, p := range []string{user, local, outside, filepath.Join(root, ".agents", "skills", "relay-builder")} {
		if _, err := os.Lstat(p); err != nil {
			t.Fatal("removed user content", p, err)
		}
	}
}

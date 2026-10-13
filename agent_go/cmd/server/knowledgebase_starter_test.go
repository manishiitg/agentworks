package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// Real Brain tools, durable state, skill retrieval and Streamable HTTP MCP.
// Existing content and deletion survive a release and a service restart.
func TestMCPProductGuideAndBrainStarter(t *testing.T) {
	t.Setenv("AUTH_SECRET", "starter-guide-test-signing-secret")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,work,relays,knowledgebase,mcp-gateway,code")
	api, service := knowledgebaseServerTest(t)
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	claims := &UserClaims{UserID: "admin", Username: "admin"}
	srv := serveExternalMCP(t, api, claims)
	cli := dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	remote := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": name, "arguments": args})
		requireRemoteSuccess(t, result, name)
		var body map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, result)), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	guide := remote("help", map[string]any{"action": "topic", "topic": "product-guide"})
	canonical := productguide.Guide(builderProductSelection(claims))
	if guide["content"] != canonical || guide["source_version"] != productguide.GuideVersion {
		t.Fatal("MCP guide differs from canonical versioned source", guide)
	}
	if strings.Contains(canonical, "### SparkQuill") || strings.Contains(canonical, "### Video Studio") {
		t.Fatal("disabled optional products advertised")
	}
	cache := remote("help", map[string]any{"action": "topic", "topic": "product-guide", "known_version": guide["version"]})
	if cache["unchanged"] != true || cache["content"] != nil {
		t.Fatal("guide cache did not retain its version")
	}
	read := remote("brain_read", map[string]any{"action": "read", "path": "Shared/AgentWorks/product-guide.md"})["result"].(map[string]any)
	if read["content"] != productguide.Guide(productpolicy.Selection{}) || read["version"] == "" || read["version"] == productguide.GuideVersion {
		t.Fatal("Brain guide source or opaque revision incorrect", read)
	}
	const skillFolder = "Shared/Skills/agentworks-mcp"
	pack := remote("brain_skills", map[string]any{"action": "get", "folder_path": skillFolder})["result"].(map[string]any)
	if pack["name"] != productguide.SkillName || pack["version"] == "" || len(pack["files"].([]any)) != 3 {
		t.Fatal("Brain package incomplete", pack)
	}
	files := map[string]string{}
	for _, raw := range pack["files"].([]any) {
		file := raw.(map[string]any)
		files[file["path"].(string)] = file["content"].(string)
	}
	if files["SKILL.md"] != productguide.Skill(productpolicy.Selection{}) || files["references/products.md"] != read["content"] {
		t.Fatal("Brain skill and guide drifted from shared sources")
	}
	var versions map[string]map[string]string
	if err := json.Unmarshal([]byte(files["references/versions.json"]), &versions); err != nil {
		t.Fatal(err)
	}
	if versions["mcp_skill"]["sha256"] != productguide.Fingerprint(files["SKILL.md"]) || versions["product_guide"]["sha256"] != productguide.Fingerprint(files["references/products.md"]) {
		t.Fatal("package content fingerprints do not match actual files")
	}
	guidancePack := remote("help", map[string]any{"action": "skills", "topics": []any{productguide.SkillName}})
	var packageVersion string
	for _, raw := range guidancePack["skills"].([]any) {
		item := raw.(map[string]any)
		if item["name"] == productguide.SkillName {
			packageVersion = item["version"].(string)
			if item["source_version"] != productguide.SkillVersion || len(item["files"].([]any)) != 3 {
				t.Fatal("MCP usage skill omitted references or version")
			}
		}
	}
	if packageVersion == "" {
		t.Fatal("MCP usage skill not discoverable")
	}
	cachedPack := remote("help", map[string]any{"action": "skills", "topics": []any{productguide.SkillName}, "known_versions": map[string]any{productguide.SkillName: packageVersion}})
	for _, raw := range cachedPack["skills"].([]any) {
		item := raw.(map[string]any)
		if item["name"] == productguide.SkillName && item["unchanged"] != true {
			t.Fatal("skill package cache missed")
		}
	}
	install := t.TempDir()
	if _, err := agentworksclient.InstallSkill(install, false); err != nil {
		t.Fatal(err)
	}
	if _, err := agentworksclient.InstallSkill(install, false); err == nil {
		t.Fatal("CLI overwrote an existing skill without force")
	}
	if content, err := os.ReadFile(filepath.Join(install, "agentworks", "references", "products.md")); err != nil || string(content) != files["references/products.md"] {
		t.Fatal("CLI product reference missing or different", err)
	}
	if !strings.Contains(agentworksclient.EntrySkill(), "| Goals |") || !strings.Contains(agentworksclient.EntrySkill(), "| Brain |") {
		t.Fatal("entry skill lacks product routing overview")
	}
	// Customize one starter and deliberately remove another through native tools.
	remote("brain_update", map[string]any{"action": "update", "path": "Shared/AgentWorks/product-guide.md", "content": "Our own product guide", "expected_version": read["version"], "request_id": "custom-guide"})
	ref := remote("brain_read", map[string]any{"action": "read", "path": skillFolder + "/references/products.md"})["result"].(map[string]any)
	remote("brain_update", map[string]any{"action": "delete", "path": skillFolder + "/references/products.md", "expected_version": ref["version"], "request_id": "remove-starter-ref"})
	config, err := knowledgebaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := knowledgebase.New(config)
	if err != nil {
		t.Fatal(err)
	}
	knowledgebaseInstance.Lock()
	knowledgebaseInstance.service = restarted
	knowledgebaseInstance.Unlock()
	if err := seedKnowledgebaseStarter(t.Context(), restarted, admin); err != nil {
		t.Fatal(err)
	}
	if value, err := restarted.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": "Shared/AgentWorks/product-guide.md"}); err != nil || value.(map[string]any)["content"] != "Our own product guide" {
		t.Fatal("customized note replaced after restart", value, err)
	}
	if _, err := restarted.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": skillFolder + "/references/products.md"}); !brainStarterError(err, "NOT_FOUND") {
		t.Fatal("deleted starter reference resurrected", err)
	}

	t.Run("preexisting-user-package", func(t *testing.T) {
		s, err := knowledgebase.New(knowledgebase.Config{Root: filepath.Join(t.TempDir(), "brain"), OrganizationID: "starter-test"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.SyncPlatformIdentities(t.Context(), []knowledgebase.Identity{{ID: "admin", Name: "Admin", Type: "user"}}); err != nil {
			t.Fatal(err)
		}
		if err := s.EnsureDefaultFolder(t.Context(), admin, "Shared", "Editor", nil); err != nil {
			t.Fatal(err)
		}
		if err := ensureBrainStarterParents(t.Context(), s, admin, skillFolder); err != nil {
			t.Fatal(err)
		}
		_, err = s.CallTool(t.Context(), admin, "brain_update", map[string]any{"action": "create", "folder_path": skillFolder, "filename": "SKILL.md", "type": "skill", "title": "Custom MCP skill", "content": "Our own skill", "request_id": "existing-skill"})
		if err != nil {
			t.Fatal(err)
		}
		if err := seedBrainStarterFiles(t.Context(), s, admin, t.TempDir(), knowledgebaseStarterFiles()); err != nil {
			t.Fatal(err)
		}
		if value, err := s.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": skillFolder + "/SKILL.md"}); err != nil || value.(map[string]any)["content"] != "Our own skill" {
			t.Fatal("preexisting skill replaced", value, err)
		}
		if _, err := s.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": skillFolder + "/references/products.md"}); !brainStarterError(err, "NOT_FOUND") {
			t.Fatal("platform mixed references into a user skill", err)
		}
	})
}

package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Real Brain tools, durable state, skill retrieval and Streamable HTTP MCP.
// Existing content and deletion survive a release and a service restart.
func TestMCPProductGuideAndBrainStarter(t *testing.T) {
	t.Setenv("AUTH_SECRET", "starter-guide-test-signing-secret")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work,relays,knowledgebase,mcp-gateway,code")
	// The external help dispatcher inventories workflows before routing even a
	// Brain-only call. Supply the real document handler and an empty workspace.
	docs := t.TempDir()
	if err := os.Mkdir(filepath.Join(docs, "Workflow"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api, service := knowledgebaseServerTest(t)
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token, _, err := store.Issue(t.Context(), accesstokens.Token{UserID: "admin", Username: "admin", Name: "starter_mcp", Scopes: []string{"knowledgebase:read", "knowledgebase:write"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "admin", Username: "admin", AccessToken: &token}
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
	for _, excluded := range []string{"Goals", "ExampleApp", "Example Product", "AgentBridge"} {
		if strings.Contains(canonical, excluded) {
			t.Fatalf("unavailable or personal product %s advertised in the guide", excluded)
		}
	}
	if !strings.Contains(canonical, "### Should I use a Crew or a Flow?") || strings.Contains(canonical, "### Should I use Goals or") {
		t.Fatal("product-choice FAQ does not match installation availability")
	}
	cache := remote("help", map[string]any{"action": "topic", "topic": "product-guide", "known_version": guide["version"]})
	if cache["unchanged"] != true || cache["content"] != nil {
		t.Fatal("guide cache did not retain its version")
	}
	setup := remote("help", map[string]any{"action": "topic", "topic": productguide.CLISetupTopic})
	if setup["content"] != productguide.CLISetup(builderProductSelection(claims)) || setup["source_version"] != productguide.CLISetupVersion {
		t.Fatal("MCP CLI setup differs from its versioned source", setup)
	}
	setupNote := remote("brain", map[string]any{"action": "read", "path": "Shared/AgentWorks/cli-setup.md"})["result"].(map[string]any)
	if setupNote["content"] != productguide.CLISetup(productpolicy.Selection{}) {
		t.Fatal("Brain CLI setup differs from the shared source", setupNote)
	}
	read := remote("brain", map[string]any{"action": "read", "path": "Shared/AgentWorks/product-guide.md"})["result"].(map[string]any)
	noteVersion, _ := read["version"].(string)
	if read["content"] != productguide.Guide(productpolicy.Selection{}) || noteVersion == "" || noteVersion == productguide.GuideVersion {
		t.Fatal("Brain guide source or opaque revision incorrect", read)
	}
	const skillFolder = "Shared/Skills/agentworks-mcp"
	pack := remote("brain", map[string]any{"action": "skills_get", "folder_path": skillFolder})["result"].(map[string]any)
	brainSkillVersion, _ := pack["version"].(string)
	if pack["name"] != productguide.SkillName || brainSkillVersion == "" || len(pack["files"].([]any)) != 4 {
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
	if files["references/cli-setup.md"] != setupNote["content"] {
		t.Fatal("Brain note and installable CLI setup reference drifted")
	}
	var versions map[string]map[string]string
	if err := json.Unmarshal([]byte(files["references/versions.json"]), &versions); err != nil {
		t.Fatal(err)
	}
	if versions["mcp_skill"]["sha256"] != productguide.Fingerprint(files["SKILL.md"]) || versions["product_guide"]["sha256"] != productguide.Fingerprint(files["references/products.md"]) {
		t.Fatal("package content fingerprints do not match actual files")
	}
	if versions["cli_setup"]["version"] != productguide.CLISetupVersion || versions["cli_setup"]["sha256"] != productguide.Fingerprint(files["references/cli-setup.md"]) {
		t.Fatal("CLI setup version/fingerprint does not describe the packaged source")
	}
	guidancePack := remote("help", map[string]any{"action": "skills", "topics": []any{productguide.SkillName}})
	var packageVersion string
	for _, raw := range guidancePack["skills"].([]any) {
		item := raw.(map[string]any)
		if item["name"] == productguide.SkillName {
			packageVersion = item["version"].(string)
			if item["source_version"] != productguide.SkillVersion || len(item["files"].([]any)) != 4 {
				t.Fatal("MCP usage skill omitted references or version")
			}
			for _, rawFile := range item["files"].([]any) {
				file := rawFile.(map[string]any)
				if file["path"] == "references/cli-setup.md" && file["content"] != setup["content"] {
					t.Fatal("MCP package CLI setup differs from the topic")
				}
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
	if content, err := os.ReadFile(filepath.Join(install, "agentworks", "references", "cli-setup.md")); err != nil || string(content) != setupNote["content"] {
		t.Fatal("CLI setup reference missing or different", err)
	}
	if strings.Contains(agentworksclient.EntrySkill(), "| Goals |") || !strings.Contains(agentworksclient.EntrySkill(), "| Crew |") || !strings.Contains(agentworksclient.EntrySkill(), "| Brain |") {
		t.Fatal("entry skill lacks product routing overview")
	}
	// The runtime allowlist alone must produce the same restricted guide and
	// skill as explicit surfaces, including the FAQ and supporting files.
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENT_PRODUCTS", "work,relays,knowledgebase,mcp-gateway,code")
	if err := seedKnowledgebaseStarter(t.Context(), service, admin); err != nil {
		t.Fatal(err)
	}
	if got := remote("help", map[string]any{"action": "topic", "topic": "product-guide"}); got["content"] != canonical {
		t.Fatal("runtime product allowlist and surface projection disagree")
	}
	for _, file := range knowledgebaseStarterFiles() {
		for _, excluded := range []string{"Goals", "ExampleApp", "Example Product", "AgentBridge"} {
			if strings.Contains(file.Content, excluded) {
				t.Fatalf("starter %s advertises unavailable or personal product %s", file.Path, excluded)
			}
		}
	}
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work,relays,knowledgebase,mcp-gateway,code")
	// A later source release refreshes only known, untouched platform copies.
	config, err := knowledgebaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(state, "brain-starters", productguide.Fingerprint(config.Root+"\x00"+config.LiveRoot+"\x00"+config.OrganizationID))
	originalFiles := knowledgebaseStarterFiles()
	futureVersion := nextStarterVersion(t, originalFiles)
	futureFiles := futureStarterFiles(originalFiles, futureVersion)
	if err := seedBrainStarterFiles(t.Context(), service, admin, stateDir, futureFiles); err != nil {
		t.Fatal(err)
	}
	read = remote("brain", map[string]any{"action": "read", "path": "Shared/AgentWorks/product-guide.md"})["result"].(map[string]any)
	if read["content"] != futureFiles[0].Content || read["version"] == noteVersion {
		t.Fatal("unchanged platform guide did not receive new version")
	}
	// Installation changes refresh the projection without requiring a new
	// semantic release, and also change the skill's guide fingerprint/version.
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work,relays,knowledgebase,mcp-gateway,code,bullpen,example-app,example-product")
	projection := futureStarterFiles(knowledgebaseStarterFiles(), futureVersion)
	beforeProjection, err := service.CallTool(t.Context(), admin, "brain_skills", map[string]any{"action": "get", "folder_path": skillFolder})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedBrainStarterFiles(t.Context(), service, admin, stateDir, projection); err != nil {
		t.Fatal(err)
	}
	afterProjection, err := service.CallTool(t.Context(), admin, "brain_skills", map[string]any{"action": "get", "folder_path": skillFolder})
	if err != nil || afterProjection.(map[string]any)["version"] == beforeProjection.(map[string]any)["version"] {
		t.Fatal("product projection did not change installable package revision", err)
	}
	for _, raw := range afterProjection.(map[string]any)["files"].([]any) {
		content := raw.(map[string]any)["content"].(string)
		if strings.Contains(content, "ExampleApp") || strings.Contains(content, "Example Product") {
			t.Fatal("personal products reached an installable Brain skill file")
		}
	}
	for _, content := range agentworksclient.EntrySkillFiles() {
		if strings.Contains(content, "ExampleApp") || strings.Contains(content, "Example Product") {
			t.Fatal("personal products reached an installed CLI skill file")
		}
	}
	futureFiles = projection
	_ = cli.Close()
	cli = dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	read = remote("brain", map[string]any{"action": "read", "path": "Shared/AgentWorks/product-guide.md"})["result"].(map[string]any)
	if !strings.Contains(read["content"].(string), "### AgentBridge") || strings.Contains(read["content"].(string), "ExampleApp") || strings.Contains(read["content"].(string), "Example Product") {
		t.Fatal("enabled work product or personal-product exclusion incorrect in Brain guide")
	}
	enabledSetup := remote("help", map[string]any{"action": "topic", "topic": productguide.CLISetupTopic})
	if enabledSetup["content"] != productguide.CLISetup(builderProductSelection(claims)) || !strings.Contains(enabledSetup["content"].(string), "## Connect the coding chat to AgentBridge") {
		t.Fatal("enabled AgentBridge setup unavailable through MCP", enabledSetup)
	}
	if err := seedBrainStarterFiles(t.Context(), service, admin, stateDir, originalFiles); err != nil {
		t.Fatal(err)
	}
	afterRollback := remote("brain", map[string]any{"action": "read", "path": "Shared/AgentWorks/product-guide.md"})["result"].(map[string]any)
	if afterRollback["content"] != read["content"] {
		t.Fatal("older source downgraded a newer guide")
	}
	// Customize one starter and deliberately remove another through native tools.
	remote("brain", map[string]any{"action": "update", "path": "Shared/AgentWorks/product-guide.md", "content": "Our own product guide", "expected_version": read["version"], "request_id": "custom-guide"})
	setupNote = remote("brain", map[string]any{"action": "read", "path": "Shared/AgentWorks/cli-setup.md"})["result"].(map[string]any)
	remote("brain", map[string]any{"action": "update", "path": "Shared/AgentWorks/cli-setup.md", "content": "Our own CLI notes", "expected_version": setupNote["version"], "request_id": "custom-cli-setup"})
	ref := remote("brain", map[string]any{"action": "read", "path": skillFolder + "/references/products.md"})["result"].(map[string]any)
	remote("brain", map[string]any{"action": "delete", "path": skillFolder + "/references/products.md", "expected_version": ref["version"], "request_id": "remove-starter-ref"})
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
	if err := seedBrainStarterFiles(t.Context(), restarted, admin, stateDir, futureStarterFiles(futureFiles, nextStarterVersion(t, futureFiles))); err != nil {
		t.Fatal(err)
	}
	if value, err := restarted.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": "Shared/AgentWorks/product-guide.md"}); err != nil || value.(map[string]any)["content"] != "Our own product guide" {
		t.Fatal("customized note replaced after restart", value, err)
	}
	if value, err := restarted.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": "Shared/AgentWorks/cli-setup.md"}); err != nil || value.(map[string]any)["content"] != "Our own CLI notes" {
		t.Fatal("customized CLI setup note replaced after restart", value, err)
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
		if err := ensureBrainStarterParents(t.Context(), s, admin, "Shared/AgentWorks"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CallTool(t.Context(), admin, "brain_update", map[string]any{"action": "create", "folder_path": "Shared/AgentWorks", "filename": "cli-setup.md", "type": "note", "title": "Our setup", "content": "Existing user setup notes", "request_id": "existing-cli-setup"}); err != nil {
			t.Fatal(err)
		}
		if err := seedBrainStarterFiles(t.Context(), s, admin, t.TempDir(), knowledgebaseStarterFiles()); err != nil {
			t.Fatal(err)
		}
		if value, err := s.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": skillFolder + "/SKILL.md"}); err != nil || value.(map[string]any)["content"] != "Our own skill" {
			t.Fatal("preexisting skill replaced", value, err)
		}
		for _, reference := range []string{"products.md", "cli-setup.md"} {
			if _, err := s.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": skillFolder + "/references/" + reference}); !brainStarterError(err, "NOT_FOUND") {
				t.Fatal("platform mixed references into a user skill", reference, err)
			}
		}
		if value, err := s.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": "Shared/AgentWorks/cli-setup.md"}); err != nil || value.(map[string]any)["content"] != "Existing user setup notes" {
			t.Fatal("preexisting CLI setup note replaced", value, err)
		}
	})
	t.Run("definite-refusal-retries", func(t *testing.T) {
		s, err := knowledgebase.New(knowledgebase.Config{Root: filepath.Join(t.TempDir(), "brain"), OrganizationID: "refusal-test"})
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
		caller := &refuseStarterOnce{brainStarterCaller: s}
		offers := t.TempDir()
		files := originalFiles[:1]
		if err := seedBrainStarterFiles(t.Context(), caller, admin, offers, files); !brainStarterError(err, "FORBIDDEN") {
			t.Fatal("expected pre-write refusal", err)
		}
		if err := seedBrainStarterFiles(t.Context(), caller, admin, offers, files); err != nil {
			t.Fatal(err)
		}
		result, err := s.CallTool(t.Context(), admin, "brain_read", map[string]any{"action": "read", "path": files[0].Path})
		if err != nil || result.(map[string]any)["content"] != files[0].Content {
			t.Fatal("refused first attempt permanently suppressed starter", result, err)
		}
	})
}

type refuseStarterOnce struct {
	brainStarterCaller
	refused bool
}

func (c *refuseStarterOnce) CallTool(ctx context.Context, p knowledgebase.Principal, name string, args map[string]any) (any, error) {
	if name == "brain_update" && args["action"] == "create" && !c.refused {
		c.refused = true
		return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "temporary pre-write refusal"}
	}
	return c.brainStarterCaller.CallTool(ctx, p, name, args)
}

// Keep simulated releases ahead of every current source version.
func nextStarterVersion(t *testing.T, files []brainStarterFile) string {
	t.Helper()
	major := 0
	for _, file := range files {
		value, _, _ := strings.Cut(file.SourceVersion, ".")
		n, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		major = max(major, n)
	}
	return strconv.Itoa(major+1) + ".0.0"
}

// Render a future release fixture with coherent source versions and hashes.
func futureStarterFiles(original []brainStarterFile, version string) []brainStarterFile {
	out := append([]brainStarterFile(nil), original...)
	oldGuide, oldSkill := original[0].Content, original[1].Content
	guide := strings.Replace(oldGuide, "Guide version: "+original[0].SourceVersion, "Guide version: "+version, 1)
	skill := strings.ReplaceAll(oldSkill, original[1].SourceVersion, version)
	skill = strings.ReplaceAll(skill, productguide.Fingerprint(oldGuide), productguide.Fingerprint(guide))
	setup := strings.Replace(original[4].Content, "CLI setup guide version: "+original[4].SourceVersion, "CLI setup guide version: "+version, 1)
	manifest, _ := json.Marshal(map[string]any{
		"product_guide": map[string]string{"version": version, "sha256": productguide.Fingerprint(guide)},
		"mcp_skill":     map[string]string{"name": productguide.SkillName, "version": version, "sha256": productguide.Fingerprint(skill)},
		"cli_setup":     map[string]string{"version": version, "sha256": productguide.Fingerprint(setup)},
	})
	for i := range out {
		out[i].SourceVersion = version
		switch i {
		case 0, 2:
			out[i].Content = guide
		case 1:
			out[i].Content = skill
		case 3:
			out[i].Content = string(manifest) + "\n"
		case 4, 5:
			out[i].Content = setup
		}
	}
	return out
}

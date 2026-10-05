package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalSkillRequiresIdentity(t *testing.T) {
	api := &StreamingAPI{}
	for path, handler := range map[string]func(http.ResponseWriter, *http.Request){
		"/api/external/v1/skill.md":          api.handleExternalSkillMD,
		"/api/external/v1/skill.zip":         api.handleExternalSkillZIP,
		"/api/external/v1/agentworks.plugin": api.handleExternalPlugin,
	} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", path, w.Code)
		}
	}
}

func TestExternalPluginPackagesSkillAndOAuthConnector(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://confida.example.com")
	api := &StreamingAPI{}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	w := httptest.NewRecorder()
	api.handleExternalPlugin(w, adminRequest(http.MethodGet, "/api/external/v1/agentworks.plugin", "", claims, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("plugin status %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/zip" || !strings.Contains(w.Header().Get("Content-Disposition"), "agentworks.plugin") {
		t.Fatalf("wrong plugin download headers: %v", w.Header())
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, file := range archive.File {
		body, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(body)
		body.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = string(content)
	}
	if len(entries) != 4 {
		t.Fatalf("unexpected plugin files: %v", entries)
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(entries[".claude-plugin/plugin.json"]), &manifest); err != nil || manifest.Name != "agentworks" || manifest.Version == "" {
		t.Fatalf("invalid plugin manifest: %+v %v", manifest, err)
	}
	var connector struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(entries[".mcp.json"]), &connector); err != nil {
		t.Fatal(err)
	}
	if server := connector.MCPServers["agentworks"]; server.Type != "http" || server.URL != "https://confida.example.com/api/external/v1/mcp" {
		t.Fatalf("invalid MCP connector: %+v", server)
	}
	if entries["skills/agentworks/SKILL.md"] != buildHostedSkillMarkdown("https://confida.example.com") {
		t.Fatal("plugin skill differs from hosted skill")
	}
	if !strings.Contains(entries["README.md"], "OAuth") {
		t.Fatal("plugin instructions missing")
	}
	for name, content := range entries {
		if strings.Contains(content, "aw_pat_") || strings.Contains(content, "AGENTWORKS_TOKEN") {
			t.Fatalf("credential reference in %s", name)
		}
	}
}

func TestExternalPluginRequiresPublicHTTPSURL(t *testing.T) {
	t.Setenv("PUBLIC_URL", "http://localhost:18743")
	w := httptest.NewRecorder()
	(&StreamingAPI{}).handleExternalPlugin(w, adminRequest(http.MethodGet, "/api/external/v1/agentworks.plugin", "", &UserClaims{UserID: "owner"}, nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("local plugin status %d, want 503", w.Code)
	}
}

func TestExternalSkillMDAndZIP(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://skills.test")
	api := &StreamingAPI{}
	claims := &UserClaims{UserID: "owner", Username: "owner"}

	mdRec := httptest.NewRecorder()
	api.handleExternalSkillMD(mdRec, adminRequest(http.MethodGet, "/api/external/v1/skill.md", "", claims, nil))
	if mdRec.Code != http.StatusOK {
		t.Fatalf("skill.md status %d: %s", mdRec.Code, mdRec.Body.String())
	}
	if ctype := mdRec.Header().Get("Content-Type"); !strings.HasPrefix(ctype, "text/markdown") {
		t.Fatalf("skill.md content type %q", ctype)
	}
	markdown := mdRec.Body.String()
	for _, want := range []string{
		"---\nname: agentworks\n",
		"description: " + hostedSkillDescription,
		"https://skills.test",
		"get_agent_context",
		"browse_knowledgebase", "read_knowledgebase", "update_knowledgebase", "backup_knowledgebase", "manage_knowledgebase_access", "agentworks-local",
		"list_workflows",
		"run_status",
		"If Builder is absent",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("skill.md missing %q", want)
		}
	}
	if strings.Contains(markdown, "aw_pat_") || strings.Contains(markdown, "agentworks login") || strings.Contains(markdown, "claude mcp add") {
		t.Fatal("hosted skill must not carry credentials or local-CLI setup")
	}
	if len(hostedSkillDescription) > 1024 || strings.ContainsAny(hostedSkillDescription, "<>") {
		t.Fatal("skill frontmatter description violates upload-scanner constraints")
	}

	zipRec := httptest.NewRecorder()
	api.handleExternalSkillZIP(zipRec, adminRequest(http.MethodGet, "/api/external/v1/skill.zip", "", claims, nil))
	if zipRec.Code != http.StatusOK {
		t.Fatalf("skill.zip status %d: %s", zipRec.Code, zipRec.Body.String())
	}
	if ctype := zipRec.Header().Get("Content-Type"); ctype != "application/zip" {
		t.Fatalf("skill.zip content type %q", ctype)
	}
	archive, err := zip.NewReader(bytes.NewReader(zipRec.Body.Bytes()), int64(zipRec.Body.Len()))
	if err != nil {
		t.Fatalf("unzip: %v", err)
	}
	if len(archive.File) != 1 || archive.File[0].Name != "agentworks/SKILL.md" {
		names := make([]string, 0, len(archive.File))
		for _, f := range archive.File {
			names = append(names, f.Name)
		}
		t.Fatalf("zip entries %v, want [agentworks/SKILL.md]", names)
	}
	entry, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	zipped, err := io.ReadAll(entry)
	if err != nil {
		t.Fatal(err)
	}
	if string(zipped) != markdown {
		t.Fatal("zipped SKILL.md differs from served skill.md")
	}
}

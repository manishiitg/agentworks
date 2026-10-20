package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/externalguidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// Hosted skill endpoints. ChatGPT and Claude Cowork connect over remote MCP
// and have no skill directory, so the Connect tab offers the AgentWorks skill
// as a downloadable zip (Plugins/Skills upload) plus the same text for
// pasting into Custom Instructions / connector instructions on plans without
// Skills support. The zip follows the Agent Skills SKILL.md layout that
// ChatGPT, Claude, and Cowork all accept for upload.
//
// The skill carries the server origin but never a credential. Hosted clients
// obtain access through OAuth; skill text can be shared across workspaces.

// hostedSkillDescription is the SKILL.md frontmatter description: what the
// skill does and when to use it. Keep it under 1024 chars with no XML
// brackets (frontmatter constraints shared by the upload scanners).
var hostedSkillDescription = func() string {
	metadata, _, err := externalguidance.Parse([]byte(agentworksclient.EntrySkill()))
	if err != nil {
		panic(err)
	}
	return metadata.Description
}()

// buildHostedSkillMarkdown renders the hosted SKILL.md. It must stay
// self-contained: ChatGPT delivers tools only (no MCP prompts, resources, or
// initialize instructions), so a hosted agent sees exactly this text plus
// tool schemas. It shares sections with
// agent_go/pkg/agentworksclient/skills/agentworks/SKILL.md, but the dispatch
// differs: the remote surface exposes only get_api_spec and call_tool, so
// every tool below runs through call_tool.
func buildHostedSkillMarkdown(origin string) string {
	return agentworksclient.EntrySkill() + "\nServer: " + strings.TrimRight(origin, "/") + "\nMCP endpoint: " + strings.TrimRight(origin, "/") + externalMCPPath + "\n"
}

func (api *StreamingAPI) handleExternalSkillMD(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, buildHostedSkillMarkdown(getBaseURL(r)))
}

// handleExternalSkillZIP serves the hosted skill as a zip for the
// Plugins/Skills upload flow (ChatGPT, Cowork).
func (api *StreamingAPI) handleExternalSkillZIP(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	entry, err := archive.Create("agentworks/SKILL.md")
	if err != nil {
		externalError(w, http.StatusInternalServerError, "skill_unavailable", err.Error())
		return
	}
	if _, err := io.WriteString(entry, buildHostedSkillMarkdown(getBaseURL(r))); err != nil {
		externalError(w, http.StatusInternalServerError, "skill_unavailable", err.Error())
		return
	}
	for _, name := range []string{"references/products.md", "references/cli-setup.md", "references/versions.json"} {
		content := agentworksclient.EntrySkillFiles()[name]
		if name == "references/versions.json" {
			content = productguide.VersionsForSkill(productpolicy.Selection{}, "agentworks", agentworksclient.EntrySkillVersion, buildHostedSkillMarkdown(getBaseURL(r)))
		}
		entry, err := archive.Create("agentworks/" + name)
		if err == nil {
			_, err = io.WriteString(entry, content)
		}
		if err != nil {
			externalError(w, http.StatusInternalServerError, "skill_unavailable", err.Error())
			return
		}
	}
	if err := archive.Close(); err != nil {
		externalError(w, http.StatusInternalServerError, "skill_unavailable", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="agentworks-skill.zip"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// handleExternalPlugin packages the hosted skill with a Cowork remote MCP
// connector. The OAuth client discovers authorization from the public MCP URL;
// no credential or client secret belongs in the downloadable archive.
func (api *StreamingAPI) handleExternalPlugin(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	// mcpOAuthURLs also admits a loopback URL (local MCP sign-in); a plugin
	// is installed elsewhere, so it needs the public HTTPS URL.
	origin, resource, ok := mcpOAuthURLs()
	if !ok || !strings.HasPrefix(origin, "https://") {
		externalError(w, http.StatusServiceUnavailable, "plugin_unavailable", "The Cowork plugin requires a configured public HTTPS URL.")
		return
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"name": "agentworks", "version": "0.2.2",
		"description": "Choose and use AgentWorks products through current MCP tools, roles and consent.",
		"author":      map[string]string{"name": "AgentWorks"},
	}, "", "  ")
	if err != nil {
		externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
		return
	}
	connector, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{"agentworks": map[string]string{"type": "http", "url": resource}},
	}, "", "  ")
	if err != nil {
		externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
		return
	}
	files := []struct{ name, content string }{
		{".claude-plugin/plugin.json", string(manifest) + "\n"},
		{".mcp.json", string(connector) + "\n"},
		{"skills/agentworks/SKILL.md", buildHostedSkillMarkdown(origin)},
		{"README.md", "# AgentWorks for Claude Cowork\n\nInstall this plugin in Customize > Plugins, then connect AgentWorks and approve access in your browser. The connector uses OAuth; this package contains no credential.\n"},
	}
	for _, name := range []string{"references/products.md", "references/cli-setup.md", "references/versions.json"} {
		content := agentworksclient.EntrySkillFiles()[name]
		if name == "references/versions.json" {
			content = productguide.VersionsForSkill(productpolicy.Selection{}, "agentworks", agentworksclient.EntrySkillVersion, buildHostedSkillMarkdown(origin))
		}
		files = append(files, struct{ name, content string }{"skills/agentworks/" + name, content})
	}
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for _, file := range files {
		entry, err := archive.Create(file.name)
		if err == nil {
			_, err = io.WriteString(entry, file.content)
		}
		if err != nil {
			externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
			return
		}
	}
	if err := archive.Close(); err != nil {
		externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="agentworks.plugin"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

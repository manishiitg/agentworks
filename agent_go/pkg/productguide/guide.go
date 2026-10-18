// Package productguide owns the product overview and MCP usage skill shared
// by external guidance, CLI installations and Brain's starter content.
package productguide

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// Bump the corresponding semantic version when changing either source.
// Fingerprints additionally identify the exact installation-specific content.
const GuideVersion = "1.0.6"
const SkillVersion = "1.0.10"
const SkillName = "agentworks-mcp"
const SkillDescription = "Choose AgentWorks products, use live MCP tools and guidance, or follow CLI installation and connection setup."
const CLISetupVersion = "1.0.2"
const CLISetupTopic = "cli-setup"
const CLISetupDescription = "Install or update the AgentWorks CLI, sign in and connect a supported coding chat."

//go:embed products.md mcp-usage.md cli-setup.md
var sources embed.FS

func source(name string) string {
	b, err := sources.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Crew is one display product available through either legacy origin. This
// projection only chooses guide text; origin entitlements stay with the server.
func selectedText(selection productpolicy.Selection, content string) string {
	origin := "work"
	if !selection.Has(origin) && selection.Has("code") {
		origin = "code"
	}
	return selection.Text(strings.ReplaceAll(content, "<!-- product:crew -->", "<!-- product:"+origin+" -->"))
}

func Fingerprint(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func Guide(selection productpolicy.Selection) string {
	body := selectedText(selection, source("products.md"))
	return "# AgentWorks product guide\n\nGuide version: " + GuideVersion + "\n\n" + body
}

// Overview is the short routing table from the same source as the full guide.
func Overview(selection productpolicy.Selection) string {
	body := selectedText(selection, source("products.md"))
	overview, _, _ := strings.Cut(body, "## Product details")
	return strings.TrimSpace(overview)
}

func SkillBody(selection productpolicy.Selection) string {
	return "Skill version: " + SkillVersion + "; product guide version: " + GuideVersion + ".\n\n" +
		selectedText(selection, source("mcp-usage.md"))
}

func CLISetup(selection productpolicy.Selection) string {
	return "CLI setup guide version: " + CLISetupVersion + ".\n\n" + selection.Text(source("cli-setup.md"))
}

func Skill(selection productpolicy.Selection) string {
	return "---\nname: " + SkillName + "\ndescription: " + SkillDescription + "\nmetadata:\n  version: " + SkillVersion + "\n  product-guide-version: " + GuideVersion + "\n  product-guide-sha256: \"" + Fingerprint(Guide(selection)) + "\"\n---\n\n" + SkillBody(selection)
}

// Versions separates human-readable source releases from exact content hashes
// and Brain's opaque entry revisions, which are used for concurrency checks.
func Versions(selection productpolicy.Selection) string {
	return VersionsForSkill(selection, SkillName, SkillVersion, Skill(selection))
}

func VersionsForSkill(selection productpolicy.Selection, name, version, content string) string {
	b, _ := json.MarshalIndent(map[string]any{
		"product_guide": map[string]string{"version": GuideVersion, "sha256": Fingerprint(Guide(selection))},
		"mcp_skill":     map[string]string{"name": name, "version": version, "sha256": Fingerprint(content)},
		"cli_setup":     map[string]string{"version": CLISetupVersion, "sha256": Fingerprint(CLISetup(selection))},
	}, "", "  ")
	return string(b) + "\n"
}

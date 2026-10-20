package workproduct

import (
	"embed"
	"fmt"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

//go:embed product.yaml prompts/*.md skills/*/SKILL.md skills/*/references/*.md commands/*.md
var productConfigFiles embed.FS

// RunPromptTemplate is deliberately separate from the authoring feature
// extensions in the Builder prompt. Readers never receive setup instructions.
func RunPromptTemplate() string {
	text, err := agentprofiles.LoadChatPrompt(productConfigFiles, mustWorkManifest().Chat["run"].Prompt)
	if err != nil {
		panic(fmt.Errorf("read Crew Run prompt: %w", err))
	}
	return text
}

// ProductManifest is the shared product.yaml shape (pkg/agentprofiles).
type ProductManifest = agentprofiles.ProductManifest

var (
	productManifestOnce sync.Once
	productManifest     ProductManifest
	productManifestErr  error
)

// WorkManifest loads and validates product.yaml once.
func WorkManifest() (ProductManifest, error) {
	productManifestOnce.Do(func() {
		manifest, err := agentprofiles.LoadProductManifest(productConfigFiles, "product.yaml")
		if err != nil {
			productManifestErr = fmt.Errorf("Crew %w", err)
			return
		}
		if manifest.Profile.ID != "work" || manifest.Profile.Scope != agentprofiles.ProfileScopeProject || manifest.UI.Surface != "work" {
			productManifestErr = fmt.Errorf("invalid Crew product manifest")
			return
		}
		productManifest = manifest
	})
	return productManifest, productManifestErr
}

// BuilderExternalTools is Crew's explicit platform MCP admission list.
func BuilderExternalTools() ([]string, error) {
	manifest, err := WorkManifest()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), manifest.Chat["builder"].ExternalTools...), nil
}

func renderProductPrompt() string {
	manifest := mustWorkManifest()
	prompt, err := manifest.RenderPrompt(productConfigFiles, manifest.Profile, nil)
	if err != nil {
		panic(fmt.Errorf("render Crew prompt: %w", err))
	}
	return prompt
}

func mustWorkManifest() ProductManifest {
	manifest, err := WorkManifest()
	if err != nil {
		panic(err)
	}
	return manifest
}

const ProfileID = "work"
const ProjectsRoot = "Crew"

// CoworkSkills are attached to a Code project in Cowork mode (registered by workproduct.RegisterProductSkills).
var CoworkSkills = []string{"cowork-assistant", "cowork-browser-tasks", "cowork-automations"}

// CoworkPrompt is the system prompt of a Code project in Cowork mode (a private assistant for non-technical business users): its own
// introduction, then the same platform mechanics as Code's prompt template (the workspace, platform actions, other chats, vaults),
// so the two cannot drift apart. codeTemplate is Code's own system prompt template.
func CoworkPrompt(codeTemplate string) string {
	intro, err := productConfigFiles.ReadFile("prompts/cowork-intro.md")
	if err != nil {
		panic(fmt.Errorf("read Cowork prompt: %w", err))
	}
	body := codeTemplate
	if i := strings.Index(body, "## The workspace"); i >= 0 {
		body = body[i:]
	}
	return strings.TrimRight(string(intro), "\n") + "\n\n" + body
}

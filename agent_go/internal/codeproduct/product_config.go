package codeproduct

import (
	"embed"
	"fmt"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

//go:embed product.yaml prompts/system-prompt.md prompts/cowork-intro.md
var productConfigFiles embed.FS

// ProductManifest is the shared product.yaml shape (pkg/agentprofiles).
type ProductManifest = agentprofiles.ProductManifest

var (
	productManifestOnce sync.Once
	productManifest     ProductManifest
	productManifestErr  error
)

// CodeManifest loads and validates product.yaml once.
func CodeManifest() (ProductManifest, error) {
	productManifestOnce.Do(func() {
		manifest, err := agentprofiles.LoadProductManifest(productConfigFiles, "product.yaml")
		if err != nil {
			productManifestErr = fmt.Errorf("Code %w", err)
			return
		}
		if manifest.Profile.ID != ProfileID || manifest.Profile.Scope != agentprofiles.ProfileScopeProject || manifest.UI.Surface != ProfileID {
			productManifestErr = fmt.Errorf("invalid Code product manifest")
			return
		}
		productManifest = manifest
	})
	return productManifest, productManifestErr
}

func renderProductPrompt() string {
	manifest := mustCodeManifest()
	prompt, err := manifest.RenderPrompt(productConfigFiles, manifest.Profile, nil)
	if err != nil {
		panic(fmt.Errorf("render Code prompt: %w", err))
	}
	return prompt
}

func mustCodeManifest() ProductManifest {
	manifest, err := CodeManifest()
	if err != nil {
		panic(err)
	}
	return manifest
}

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

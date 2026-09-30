package workproduct

import (
	"embed"
	"fmt"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

//go:embed product.yaml prompts/*.md skills/*/SKILL.md commands/*.md
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

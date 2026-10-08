package agentworksproduct

import (
	"embed"
	"fmt"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

//go:embed product.yaml prompts/*.md commands/*.md
var productConfigFiles embed.FS

// ProductManifest is the shared product.yaml shape (pkg/agentprofiles).
type ProductManifest = agentprofiles.ProductManifest

var (
	productManifestOnce sync.Once
	productManifest     ProductManifest
	productManifestErr  error
)

// AgentWorksManifest loads and validates product.yaml once.
func AgentWorksManifest() (ProductManifest, error) {
	productManifestOnce.Do(func() {
		manifest, err := agentprofiles.LoadProductManifest(productConfigFiles, "product.yaml")
		if err != nil {
			productManifestErr = fmt.Errorf("AgentWorks %w", err)
			return
		}
		if manifest.Profile.ID != "agentworks" || manifest.Profile.Scope != agentprofiles.ProfileScopeGlobal {
			productManifestErr = fmt.Errorf("invalid AgentWorks product manifest")
			return
		}
		if err := validateChatPolicy(manifest); err != nil {
			productManifestErr = err
			return
		}
		if err := validateChatDefinitions(productConfigFiles, manifest); err != nil {
			productManifestErr = err
			return
		}
		if err := validatePulse(manifest); err != nil {
			productManifestErr = err
			return
		}
		productManifest = manifest
	})
	return productManifest, productManifestErr
}

func renderProductPrompt() string {
	manifest := mustAgentWorksManifest()
	prompt, err := manifest.RenderPrompt(productConfigFiles, manifest.Profile, nil)
	if err != nil {
		panic(fmt.Errorf("render AgentWorks prompt: %w", err))
	}
	return prompt
}

func mustAgentWorksManifest() ProductManifest {
	manifest, err := AgentWorksManifest()
	if err != nil {
		panic(err)
	}
	return manifest
}

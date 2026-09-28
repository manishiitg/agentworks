package codeproduct

import (
	"embed"
	"fmt"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

//go:embed product.yaml prompts/system-prompt.md
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

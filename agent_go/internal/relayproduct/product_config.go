package relayproduct

import (
	"embed"
	"fmt"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

//go:embed product.yaml prompts/builder.md
var files embed.FS

var (
	once    sync.Once
	prompt  string
	loadErr error
)

// BuilderPrompt is added to the shared AgentWorks workflow Builder when the
// workflow manifest is a Relay. The manifest is the source of the product
// contract; execution and tool admission stay on existing workflow paths.
func BuilderPrompt() (string, error) {
	once.Do(func() {
		manifest, err := agentprofiles.LoadProductManifest(files, "product.yaml")
		if err != nil {
			loadErr = fmt.Errorf("Relay product manifest: %w", err)
			return
		}
		if manifest.Profile.ID != "relays" {
			loadErr = fmt.Errorf("Relay product manifest has profile %q", manifest.Profile.ID)
			return
		}
		prompt, loadErr = manifest.RenderPrompt(files, manifest.Profile, nil)
	})
	return prompt, loadErr
}

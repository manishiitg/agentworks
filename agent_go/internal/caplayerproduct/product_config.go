package caplayerproduct

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"reflect"
	"sync"
)

var manifestOnce sync.Once
var manifest agentprofiles.ProductManifest
var manifestErr error

// Manifest validates both transports against one canonical tool admission list.
func Manifest() agentprofiles.ProductManifest {
	manifestOnce.Do(func() {
		manifest, manifestErr = agentprofiles.LoadProductManifest(files, "product.yaml")
		if manifestErr != nil {
			return
		}
		tools := manifest.Chat["builder"].Tools
		if manifest.Profile.ID != ProfileID || manifest.Profile.Runtime.Workspace.Root != WorkspaceRoot ||
			!reflect.DeepEqual(tools, manifest.Chat["builder"].ExternalTools) ||
			!reflect.DeepEqual(tools, manifest.Profile.ToolPolicy.Enabled) ||
			!reflect.DeepEqual(tools, manifest.Profile.Runtime.BridgeTools) || len(tools) != len(manifest.Profile.Tools) {
			manifestErr = fmt.Errorf("Vault product.yaml builder, bridge and external tool declarations must agree")
		}
	})
	if manifestErr != nil {
		panic(manifestErr)
	}
	return manifest
}

func ExternalTools() []string {
	return append([]string(nil), Manifest().Chat["builder"].ExternalTools...)
}

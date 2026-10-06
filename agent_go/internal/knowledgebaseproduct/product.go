package knowledgebaseproduct

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

const ProfileID = "knowledgebase"

//go:embed product.yaml prompts/*.md commands/*.md
var files embed.FS

var once sync.Once
var manifest agentprofiles.ProductManifest
var manifestErr error

func Manifest() (agentprofiles.ProductManifest, error) {
	once.Do(func() {
		manifest, manifestErr = agentprofiles.LoadProductManifest(files, "product.yaml")
		if manifestErr == nil && (manifest.Profile.ID != ProfileID || manifest.UI.Surface != ProfileID) {
			manifestErr = fmt.Errorf("invalid Brain product identity")
		}
	})
	return manifest, manifestErr
}

func BuiltinAgentProfile() agentprofiles.Profile {
	m, err := Manifest()
	if err != nil {
		panic(err)
	}
	p := m.Profile
	p.Product = ProfileID
	p.SystemPromptTemplate, err = m.RenderPrompt(files, p, nil)
	if err != nil {
		panic(err)
	}
	return p
}

func ExternalTools() ([]string, error) {
	m, err := Manifest()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), m.Chat["mcp"].ExternalTools...), nil
}

// The server supplies the execution principal; arguments never select one.
type AccessExecutor func(context.Context, agentprofiles.ToolRuntimeContext, map[string]any) (string, error)

func RegisterAgentProfileRuntime(registry *agentprofiles.Registry, execute AccessExecutor) error {
	factories := []struct {
		id, name string
		execute  AccessExecutor
	}{{"knowledgebase.manage-access", knowledgebase.ToolAccess, execute}}
	for _, factory := range factories {
		factory := factory
		if err := registry.RegisterToolFactory(factory.id, func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
			for _, def := range knowledgebase.ToolDefinitions() {
				if def.Name == factory.name {
					return agentprofiles.ToolSpec{Name: def.Name, Description: def.Description, Parameters: def.InputSchema, Category: ProfileID, Execute: func(ctx context.Context, args map[string]any) (string, error) {
						if factory.execute == nil {
							return "", fmt.Errorf("Brain access executor unavailable")
						}
						return factory.execute(ctx, runtime, args)
					}}, nil
				}
			}
			return agentprofiles.ToolSpec{}, fmt.Errorf("Brain tool is not registered")
		}); err != nil {
			return err
		}
	}
	return nil
}

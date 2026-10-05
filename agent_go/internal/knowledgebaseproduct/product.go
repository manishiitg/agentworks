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

//go:embed product.yaml prompts/*.md
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

func RegisterAgentProfileRuntime(registry *agentprofiles.Registry, execute AccessExecutor, backup ...AccessExecutor) error {
	factories := []struct {
		id, name string
		execute  AccessExecutor
	}{{"knowledgebase.manage-access", "manage_knowledgebase_access", execute}}
	var gitExecutor AccessExecutor
	if len(backup) > 0 {
		gitExecutor = backup[0]
	}
	factories = append(factories, struct {
		id, name string
		execute  AccessExecutor
	}{"knowledgebase.git", "backup_knowledgebase", gitExecutor})
	for _, factory := range factories {
		factory := factory
		if err := registry.RegisterToolFactory(factory.id, func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
			for _, def := range knowledgebase.ToolDefinitions() {
				if def.Name == factory.name {
					if factory.name == "backup_knowledgebase" {
						variants := []any{}
						for _, value := range def.InputSchema["oneOf"].([]any) {
							variant := value.(map[string]any)
							action := variant["properties"].(map[string]any)["action"].(map[string]any)["const"]
							if action == "git" || action == "status" {
								variants = append(variants, variant)
							}
						}
						def.InputSchema["oneOf"] = variants
						def.InputSchema["properties"].(map[string]any)["action"] = map[string]any{"type": "string", "enum": []any{"git", "status"}}
						def.Description = "Use the shared Files Git repository: action=git with op for diff/history, staging, commit, pull/push, branches and stashes. Pull/checkout/stash/discard update live knowledge. Repository reads require root Reader; writes require unrestricted root Editor. No content editor or shell access."
					}
					return agentprofiles.ToolSpec{Name: def.Name, Description: def.Description, Parameters: def.InputSchema, Category: ProfileID, Execute: func(ctx context.Context, args map[string]any) (string, error) {
						if factory.execute == nil {
							return "", fmt.Errorf("Brain Git executor unavailable")
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

package relayproduct

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

//go:embed product.yaml prompts/*.md skills/*/SKILL.md
var files embed.FS

var (
	once     sync.Once
	manifest agentprofiles.ProductManifest
	prompts  map[string]string
	skills   map[string]string
	loadErr  error
)

func loadProduct() error {
	once.Do(func() {
		manifest, loadErr = agentprofiles.LoadProductManifest(files, "product.yaml")
		if loadErr != nil {
			loadErr = fmt.Errorf("Relay product manifest: %w", loadErr)
			return
		}
		if manifest.Profile.ID != "relays" {
			loadErr = fmt.Errorf("Relay product manifest has profile %q", manifest.Profile.ID)
			return
		}
		if len(manifest.Chat) != 1 {
			loadErr = fmt.Errorf("Relay product must declare only builder chat")
			return
		}
		prompts = map[string]string{}
		skills = map[string]string{}
		for _, mode := range []string{"builder"} {
			def, ok := manifest.Chat[mode]
			if !ok || len(def.Skills) == 0 || len(def.Tools) == 0 {
				loadErr = fmt.Errorf("Relay %s chat needs a prompt, skills, and tools", mode)
				return
			}
			prompts[mode], loadErr = agentprofiles.LoadChatPrompt(files, def.Prompt)
			if loadErr != nil {
				return
			}
			skillSeen := map[string]bool{}
			for _, name := range def.Skills {
				if strings.Contains(name, "/") || name == "" || skillSeen[name] {
					loadErr = fmt.Errorf("invalid or duplicate Relay %s skill %q", mode, name)
					return
				}
				skillSeen[name] = true
				var raw []byte
				raw, loadErr = files.ReadFile("skills/" + name + "/SKILL.md")
				if loadErr != nil {
					return
				}
				skills[name] = string(raw)
			}
			seen := map[string]bool{}
			for _, tool := range def.Tools {
				if strings.TrimSpace(tool) == "" || seen[tool] {
					loadErr = fmt.Errorf("Relay %s chat has empty or duplicate tool %q", mode, tool)
					return
				}
				seen[tool] = true
			}
		}
	})
	return loadErr
}

func ChatPrompt(mode string) (string, error) {
	if err := loadProduct(); err != nil {
		return "", err
	}
	prompt, ok := prompts[mode]
	if !ok {
		return "", fmt.Errorf("unknown Relay chat mode %q", mode)
	}
	return prompt, nil
}

func ChatTools(mode string) ([]string, error) {
	if err := loadProduct(); err != nil {
		return nil, err
	}
	def, ok := manifest.Chat[mode]
	if !ok {
		return nil, fmt.Errorf("unknown Relay chat mode %q", mode)
	}
	return append([]string(nil), def.Tools...), nil
}

func ChatAllowsTool(mode, name string) bool {
	tools, err := ChatTools(mode)
	if err != nil {
		return false
	}
	for _, allowed := range tools {
		if allowed == name {
			return true
		}
	}
	return false
}

func ChatSkills(mode string) ([]*llmtypes.Skill, error) {
	if err := loadProduct(); err != nil {
		return nil, err
	}
	def, ok := manifest.Chat[mode]
	if !ok {
		return nil, fmt.Errorf("unknown Relay chat mode %q", mode)
	}
	out := make([]*llmtypes.Skill, 0, len(def.Skills))
	for _, name := range def.Skills {
		out = append(out, &llmtypes.Skill{
			Name: name, Description: "Instructions for Relay " + mode + " chat.",
			Content: skills[name], Source: llmtypes.SkillSource{Origin: "builtin"},
		})
	}
	return out, nil
}

func ChatDefinitionKey(mode string) (string, error) {
	prompt, err := ChatPrompt(mode)
	if err != nil {
		return "", err
	}
	tools, err := ChatTools(mode)
	if err != nil {
		return "", err
	}
	chatSkills, err := ChatSkills(mode)
	if err != nil {
		return "", err
	}
	parts := []string{prompt}
	for _, skill := range chatSkills {
		parts = append(parts, skill.Name, skill.Content)
	}
	parts = append(parts, tools...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum[:]), nil
}

// Builder aliases preserve the product contract used by existing callers.
func BuilderPrompt() (string, error)     { return ChatPrompt("builder") }
func BuilderTools() ([]string, error)    { return ChatTools("builder") }
func BuilderAllowsTool(name string) bool { return ChatAllowsTool("builder", name) }
func BuilderSkill() (*llmtypes.Skill, error) {
	skills, err := ChatSkills("builder")
	if err != nil {
		return nil, err
	}
	return skills[0], nil
}
func BuilderDefinitionKey() (string, error) { return ChatDefinitionKey("builder") }

// BuiltinAgentProfiles exposes product.yaml's command catalog through the
// existing agent-profile API. Relay execution still uses the workflow runtime.
func BuiltinAgentProfiles() ([]agentprofiles.Profile, error) {
	if err := loadProduct(); err != nil {
		return nil, err
	}
	return manifest.BuiltinProfiles(files, nil)
}

// BuilderExternalTools is the Relay-owned platform MCP admission list.
func BuilderExternalTools() ([]string, error) {
	if err := loadProduct(); err != nil {
		return nil, err
	}
	return append([]string(nil), manifest.Chat["builder"].ExternalTools...), nil
}

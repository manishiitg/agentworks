package skills

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var (
	builtinSkillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	builtinSkillsMu         sync.RWMutex
	builtinSkills           = make(map[string]*llmtypes.Skill)
)

func init() {
	err := RegisterBuiltin(&llmtypes.Skill{
		Name:        "agent-browser",
		Description: "Use managed agent_browser for interactive browsing, CDP tab ownership, downloads and capture; open when browsing is needed.",
		Content:     agentBrowserSkillContent,
		Source:      llmtypes.SkillSource{Origin: "builtin"},
	})
	if err != nil {
		panic(fmt.Sprintf("register built-in agent-browser skill: %v", err))
	}
}

// RegisterBuiltin makes a product-owned, in-memory skill available to the
// same name-based resolver used by workflow step enabled_skills. Products
// should call it during startup, before agents are launched.
//
// The registry stores and returns defensive copies so a caller cannot mutate
// another agent's skill identity through a shared pointer. RegisterBuiltin
// returns an error for invalid input or duplicate names; product startup can
// then fail with useful context instead of the shared package panicking.
func RegisterBuiltin(skill *llmtypes.Skill) error {
	if skill == nil {
		return fmt.Errorf("skill is nil")
	}
	name := strings.TrimSpace(skill.Name)
	if !builtinSkillNamePattern.MatchString(name) {
		return fmt.Errorf("invalid skill name %q: use lowercase letters, numbers, and single hyphens", skill.Name)
	}
	if strings.TrimSpace(skill.Description) == "" {
		return fmt.Errorf("skill %q has no description", name)
	}
	if strings.TrimSpace(skill.Content) == "" {
		return fmt.Errorf("skill %q has no content", name)
	}

	registered := cloneSkillDefinition(skill)
	registered.Name = name
	if strings.TrimSpace(registered.Source.Origin) == "" {
		registered.Source.Origin = "builtin"
	}

	builtinSkillsMu.Lock()
	defer builtinSkillsMu.Unlock()
	if _, exists := builtinSkills[name]; exists {
		return fmt.Errorf("builtin skill %q is already registered", name)
	}
	builtinSkills[name] = registered
	return nil
}

// AddBuiltinSupportingFile adds one reference file to an already registered
// builtin skill, so a guide owned by another package ships inside that skill
// in every product that attaches it. Call it during startup.
func AddBuiltinSupportingFile(skillName, relPath, content string) error {
	builtinSkillsMu.Lock()
	defer builtinSkillsMu.Unlock()
	skill, ok := builtinSkills[skillName]
	if !ok {
		return fmt.Errorf("builtin skill %q is not registered", skillName)
	}
	for _, file := range skill.SupportingFiles {
		if file.RelPath == relPath {
			return fmt.Errorf("builtin skill %q already has %q", skillName, relPath)
		}
	}
	skill.SupportingFiles = append(skill.SupportingFiles, llmtypes.SkillFile{RelPath: relPath, Content: []byte(content)})
	return nil
}

// IsBuiltinSkill reports whether folderName is served from the in-memory
// builtin/product registry rather than the workspace skills/ folder.
func IsBuiltinSkill(folderName string) bool {
	builtinSkillsMu.RLock()
	defer builtinSkillsMu.RUnlock()
	_, ok := builtinSkills[folderName]
	return ok
}

// IsBuiltinSkillIn reports the runtime owner for this workspace. A Crew owns
// its installed copies of another product's library, including their absence.
// Core platform procedures and the Crew's own declared defaults remain built in.
func IsBuiltinSkillIn(workspacePath, folderName string) bool {
	return builtinAttachableSkillIn(workspacePath, folderName) != nil
}

func builtinAttachableSkillIn(workspacePath, folderName string) *llmtypes.Skill {
	skill := builtinAttachableSkill(folderName)
	if skill == nil {
		return nil
	}
	if workspaceref.MustParse(workspacePath).IsShared() {
		if product := skill.Metadata["product"]; product != "" && product != "work" {
			return nil
		}
	}
	return skill
}

func builtinAttachableSkill(folderName string) *llmtypes.Skill {
	builtinSkillsMu.RLock()
	skill := builtinSkills[folderName]
	builtinSkillsMu.RUnlock()
	return cloneSkillDefinition(skill)
}

// BuiltinDefinition returns a defensive read-only copy for authorized catalog
// presentation. Reading a definition does not attach it or grant its tools.
func BuiltinDefinition(folderName string) *llmtypes.Skill {
	return builtinAttachableSkill(folderName)
}

func cloneSkillDefinition(skill *llmtypes.Skill) *llmtypes.Skill {
	if skill == nil {
		return nil
	}
	cloned := *skill
	cloned.Paths = append([]string(nil), skill.Paths...)
	if skill.Metadata != nil {
		cloned.Metadata = make(map[string]string, len(skill.Metadata))
		for key, value := range skill.Metadata {
			cloned.Metadata[key] = value
		}
	}
	if skill.SupportingFiles != nil {
		cloned.SupportingFiles = make([]llmtypes.SkillFile, len(skill.SupportingFiles))
		for index, file := range skill.SupportingFiles {
			cloned.SupportingFiles[index] = file
			cloned.SupportingFiles[index].Content = append([]byte(nil), file.Content...)
		}
	}
	return &cloned
}

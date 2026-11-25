package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// claudePluginSkill is one skill from a Claude Code plugin the person installed
// and turned on. Claude loads these itself in every chat it starts; the picker
// lists them so a person can choose one, by its plugin-qualified name.
type claudePluginSkill struct {
	Name, Description, Plugin, Path string
}

// claudeConfigHome is where Claude keeps its settings and plugins for the
// account that launches it (CLAUDE_CONFIG_DIR, else ~/.claude).
func claudeConfigHome() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func readClaudeEnabledPlugins(path string, into map[string]bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if json.Unmarshal(raw, &settings) != nil {
		return
	}
	for key, on := range settings.EnabledPlugins {
		into[key] = on
	}
}

// claudePluginSkills lists the user-invocable skills of installed, turned-on
// plugins: user-scope ones always, project/local-scope ones only for their own
// project folder. Settings layer as Claude applies them (user, project, local).
func claudePluginSkills(configHome, projectDir string) []claudePluginSkill {
	if configHome == "" {
		return nil
	}
	enabled := map[string]bool{}
	readClaudeEnabledPlugins(filepath.Join(configHome, "settings.json"), enabled)
	if projectDir != "" {
		readClaudeEnabledPlugins(filepath.Join(projectDir, ".claude", "settings.json"), enabled)
		readClaudeEnabledPlugins(filepath.Join(projectDir, ".claude", "settings.local.json"), enabled)
	}
	raw, err := os.ReadFile(filepath.Join(configHome, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &installed) != nil {
		return nil
	}
	keys := make([]string, 0, len(installed.Plugins))
	for key := range installed.Plugins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []claudePluginSkill
	for _, key := range keys {
		if !enabled[key] {
			continue
		}
		plugin, _, _ := strings.Cut(key, "@")
		for _, entry := range installed.Plugins[key] {
			if entry.Scope != "user" && (projectDir == "" || filepath.Clean(entry.ProjectPath) != filepath.Clean(projectDir)) {
				continue
			}
			files, _ := filepath.Glob(filepath.Join(entry.InstallPath, "skills", "*", "SKILL.md"))
			sort.Strings(files)
			for _, file := range files {
				content, err := os.ReadFile(file)
				if err != nil {
					continue
				}
				front, _, err := skills.ParseSkillFile(string(content))
				if err != nil || front == nil || front.UserInvocable != nil && !*front.UserInvocable {
					continue
				}
				name := firstNonEmptyTrimmed(front.Name, filepath.Base(filepath.Dir(file)))
				out = append(out, claudePluginSkill{Name: plugin + ":" + name, Description: front.Description, Plugin: plugin, Path: file})
			}
			break
		}
	}
	return out
}

package crewroot

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// One place for a Crew's files (PLAT-746-924): everything a Crew has lives in
// its .agentworks/ folder, except chats, logins and the AI program's own
// runtime, which stay in its private root. Virtual paths do not change; this
// file decides where each one lives on disk.
const (
	AutomationFolder = ".agentworks"
	// SettingsFolder holds the automation records the platform owns. It is
	// always present and never writable by programs: schedules, webhooks and
	// functions change only through their tools.
	SettingsFolder = "settings"
)

// chatPrivatePath: chats, Builder transcripts and the shell/program runtime stay private.
func chatPrivatePath(rel string) bool {
	switch strings.SplitN(strings.ToLower(rel), "/", 2)[0] {
	case "builder", "conversation", "chat_history", ".sandbox-cache", ".report-cache":
		return true
	}
	return false
}

// settingsPath: a Crew-level automation record, kept in .agentworks/settings/.
func settingsPath(rel string) bool {
	rel = strings.ToLower(rel)
	switch strings.SplitN(rel, "/", 2)[0] {
	case "product.json", "workflow.json", "workflow.json.kb-lock", "schedules.json", "webhooks.json", "product-schedules.json", "schedule-runs.json", "functions.json", "triggers":
		return true
	}
	return false
}

// automationDataPath: runtime data that lives directly in .agentworks/.
func automationDataPath(rel string) bool {
	switch strings.SplitN(strings.ToLower(rel), "/", 2)[0] {
	case "runs", "costs", "human_inputs", "planning", "knowledgebase", "db":
		return true
	}
	return false
}

// executableSettingsPath: an automation's own records, kept in
// .agentworks/settings/<runtime root>/. A script keeps its product and
// workflow records there too; a workflow's workflow.json is its definition.
func executableSettingsPath(kind, rel string) bool {
	rel = strings.ToLower(rel)
	switch strings.SplitN(rel, "/", 2)[0] {
	case "schedules.json", "webhooks.json", "functions.json", "product-schedules.json", "schedule-runs.json", "config", "publish", ".schedule-sequence", "triggers":
		return true
	case "product.json", "workflow.json", "workflow.json.kb-lock":
		return kind != "workflow"
	}
	return false
}

// Heavy runtime folders inside .agentworks. File snapshots, previews, git
// status, Files counts, Mac pairing and Team sync never walk into them.
var heavyAutomationFolders = []string{SettingsFolder, "runs", "db", "costs", "human_inputs"}
var heavyExecutableFolders = []string{"runs", "db", "learnings", "evaluation", ".cache"}

// HeavyRuntimePath reports whether rel, relative to a Crew's .agentworks
// folder, is runtime data that scans skip. It is the one exclusion list.
func HeavyRuntimePath(rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	parts := strings.Split(rel, "/")
	for _, name := range heavyAutomationFolders {
		if parts[0] == name {
			return true
		}
	}
	if len(parts) >= 3 && (parts[0] == "workflows" || parts[0] == "scripts" || parts[0] == "automations") {
		for _, name := range heavyExecutableFolders {
			if parts[2] == name {
				return true
			}
		}
		if len(parts) >= 4 && parts[2] == "planning" && (parts[3] == "changelog" || parts[3] == "revisions") {
			return true
		}
	}
	if len(parts) >= 2 && parts[0] == "planning" && (parts[1] == "changelog" || parts[1] == "revisions") {
		return true
	}
	return strings.HasSuffix(parts[len(parts)-1], ".kb-lock")
}

// HeavyCrewPath is HeavyRuntimePath for a path relative to a Crew's Files
// root, as file scans see it.
func HeavyCrewPath(rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel != AutomationFolder && !strings.HasPrefix(rel, AutomationFolder+"/") {
		return false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(rel, AutomationFolder), "/")
	return rest != "" && HeavyRuntimePath(rest)
}

// AutomationGitignore is the .agentworks/.gitignore: everything the platform
// writes stays out of the person's repository; definitions, code, knowledge,
// skills and memory stay tracked.
const AutomationGitignore = `# Written by AgentWorks: runtime data and platform records stay out of Git.
/settings/
/runs/
/db/
/costs/
/human_inputs/
/planning/changelog/
/planning/revisions/
/*/*/runs/
/*/*/db/
/*/*/learnings/
/*/*/evaluation/
/*/*/.cache/
/*/*/planning/changelog/
/*/*/planning/revisions/
*.kb-lock
.cache/
`

// EnsureAutomation creates the Crew's .agentworks/ and its settings/ folder.
// settings/ is group-readable only: the owner's slot reads records but never
// writes them. A missing .gitignore is written; an existing one gains the
// lines it lacks.
func (r Roots) EnsureAutomation() error {
	if r.Automation == "" {
		return nil
	}
	if err := os.MkdirAll(r.Automation, 0o770); err != nil {
		return err
	}
	if !r.Local {
		if info, err := os.Stat(r.Automation); err == nil && info.Mode().Perm()&0o070 != 0o070 {
			_ = os.Chmod(r.Automation, 0o770|os.ModeSetgid)
		}
	}
	settings := filepath.Join(r.Automation, SettingsFolder)
	if err := os.MkdirAll(settings, 0o750); err != nil {
		return err
	}
	if !r.Local {
		_ = os.Chmod(settings, 0o750|os.ModeSetgid)
	}
	return ensureAutomationGitignore(filepath.Join(r.Automation, ".gitignore"))
}

func ensureAutomationGitignore(file string) error {
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return os.WriteFile(file, []byte(AutomationGitignore), 0o664)
	}
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, line := range strings.Split(AutomationGitignore, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") && !have[line] {
			add = append(add, line)
		}
	}
	if len(add) == 0 {
		return nil
	}
	out := string(raw)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return os.WriteFile(file, []byte(out+strings.Join(add, "\n")+"\n"), 0o664)
}

// SettingsDir is the read-only records folder of a Crew.
func (r Roots) SettingsDir() string {
	if r.Automation == "" {
		return ""
	}
	return filepath.Join(r.Automation, SettingsFolder)
}

// settingsAlias reports a virtual relative path that names the settings folder
// directly. Records are reached by their own names (Crew/<folder>/schedules.json),
// never through settings/, so the folder cannot be written by path.
func (r Roots) settingsAlias(rel string) bool {
	rel = strings.ToLower(path.Clean("/" + filepath.ToSlash(rel)))[1:]
	alias := func(prefix string) bool { return rel == prefix || strings.HasPrefix(rel, prefix+"/") }
	if r.Automation == r.Resources && alias(SettingsFolder) {
		return true
	}
	if r.Automation == filepath.Join(r.Resources, AutomationFolder) && alias(AutomationFolder+"/"+SettingsFolder) {
		return true
	}
	return false
}

// Package productdeps provisions declarative, product-scoped dependencies.
//
// A product manifest owns what it needs; this package owns the common harness:
// validating the manifest, syncing external skill packages into the regular
// workspace skills directory, verifying CLI packages, and loading the small
// set of skills that should be attached to each agent turn.
package productdeps

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/noderuntime"
	workspaceskills "github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const statePath = ".agentworks/product-dependencies.json"

// Manifest is the reusable dependency section of a product YAML manifest.
// MCP servers are declared here so their connection details and tool policy
// travel with a product rather than becoming per-product Go conditionals.
type Manifest struct {
	Skills     []SkillSource   `yaml:"skills"`
	CLI        []CLIDependency `yaml:"cli"`
	MCPServers []MCPServer     `yaml:"mcp_servers"`
}

type SkillSource struct {
	ID           string   `yaml:"id"`
	Installer    string   `yaml:"installer"`
	Source       string   `yaml:"source"`
	Install      []string `yaml:"install"`
	Attach       []string `yaml:"attach"`
	RefreshHours int      `yaml:"refresh_hours"`
}

type CLIDependency struct {
	ID           string         `yaml:"id"`
	Package      Package        `yaml:"package"`
	Execution    CLIExecution   `yaml:"execution"`
	Verify       CLICommand     `yaml:"verify"`
	RefreshHours int            `yaml:"refresh_hours"`
	Permissions  CLIPermissions `yaml:"permissions"`
}

type Package struct {
	Ecosystem string `yaml:"ecosystem"`
	Name      string `yaml:"name"`
	Version   string `yaml:"version"`
}

type CLIExecution struct {
	Mode   string `yaml:"mode"`
	Binary string `yaml:"binary"`
}

type CLICommand struct {
	Args               []string `yaml:"args"`
	RequiredJSONChecks []string `yaml:"required_json_checks"`
}

// CLIPermissions documents the expected runtime scope for a package. The
// current native CLI runner enforces workspace isolation; these values give
// future sandboxed runners one declarative source of truth.
type CLIPermissions struct {
	Network    bool     `yaml:"network"`
	WritePaths []string `yaml:"write_paths"`
}

// MCPServer describes a future external MCP connection without placing any
// secrets in YAML. Environment values are `secret://<name>` references,
// resolved only by the
// runtime that actually starts the configured server.
type MCPServer struct {
	ID          string            `yaml:"id"`
	Enabled     bool              `yaml:"enabled"`
	Transport   string            `yaml:"transport"`
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	URL         string            `yaml:"url"`
	Environment map[string]string `yaml:"environment"`
	ToolPolicy  MCPToolPolicy     `yaml:"tool_policy"`
}

type MCPToolPolicy struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type dependencyState struct {
	Skills map[string]time.Time `json:"skills"`
	CLI    map[string]time.Time `json:"cli"`
}

// Validate fails early for malformed product declarations, before a user
// waits for an agent turn to discover that a dependency cannot be started.
func Validate(manifest Manifest) error {
	ids := map[string]struct{}{}
	for _, source := range manifest.Skills {
		if source.ID == "" || source.Installer != "skills-cli" || source.Source == "" || len(source.Install) == 0 || len(source.Attach) == 0 || source.RefreshHours < 1 {
			return fmt.Errorf("invalid external skill dependency %q", source.ID)
		}
		if err := claimID(ids, source.ID); err != nil {
			return err
		}
		installed := make(map[string]bool, len(source.Install))
		for _, name := range source.Install {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("external skill dependency %q has an empty skill name", source.ID)
			}
			installed[name] = true
		}
		for _, name := range source.Attach {
			if !installed[name] {
				return fmt.Errorf("external skill %q is attached but not installed by %q", name, source.ID)
			}
		}
	}
	for _, dependency := range manifest.CLI {
		if dependency.ID == "" || dependency.Package.Ecosystem != "npm" || dependency.Package.Name == "" || dependency.Package.Version == "" || dependency.Execution.Mode != "npx" || dependency.Execution.Binary == "" || dependency.RefreshHours < 1 {
			return fmt.Errorf("invalid CLI dependency %q", dependency.ID)
		}
		if err := claimID(ids, dependency.ID); err != nil {
			return err
		}
	}
	for _, server := range manifest.MCPServers {
		if server.ID == "" {
			return fmt.Errorf("MCP server requires an id")
		}
		if err := claimID(ids, server.ID); err != nil {
			return err
		}
		if !server.Enabled {
			continue
		}
		switch server.Transport {
		case "stdio":
			if server.Command == "" {
				return fmt.Errorf("stdio MCP server %q requires command", server.ID)
			}
		case "http", "sse":
			if server.URL == "" {
				return fmt.Errorf("%s MCP server %q requires url", server.Transport, server.ID)
			}
		default:
			return fmt.Errorf("MCP server %q has unsupported transport %q", server.ID, server.Transport)
		}
		for name, value := range server.Environment {
			if strings.TrimSpace(name) == "" || !strings.HasPrefix(value, "secret://") || strings.TrimPrefix(value, "secret://") == "" {
				return fmt.Errorf("MCP server %q environment %q must reference an AgentWorks secret with secret://<name>", server.ID, name)
			}
		}
	}
	return nil
}

func claimID(ids map[string]struct{}, id string) error {
	if _, exists := ids[id]; exists {
		return fmt.Errorf("duplicate product dependency id %q", id)
	}
	ids[id] = struct{}{}
	return nil
}

// Ensure provisions/refreshes the package types supported by the common
// harness. MCP declarations are deliberately not auto-started here: they are
// resolved by the agent runtime, where secrets and connection lifetime exist.
func Ensure(ctx context.Context, workspacePath string, manifest Manifest) error {
	if err := Validate(manifest); err != nil {
		return err
	}
	state, err := readState(workspacePath)
	if err != nil {
		return err
	}
	for _, source := range manifest.Skills {
		synced, err := syncSkillSource(ctx, workspacePath, source, state.Skills[source.ID])
		if err != nil {
			return err
		}
		if synced {
			state.Skills[source.ID] = time.Now().UTC()
		}
	}
	for _, dependency := range manifest.CLI {
		verified, err := verifyCLI(ctx, workspacePath, dependency, state.CLI[dependency.ID])
		if err != nil {
			return err
		}
		if verified {
			state.CLI[dependency.ID] = time.Now().UTC()
		}
	}
	return writeState(workspacePath, state)
}

func syncSkillSource(ctx context.Context, workspacePath string, source SkillSource, lastSync time.Time) (bool, error) {
	missing := false
	for _, name := range source.Install {
		if _, err := os.Stat(filepath.Join(workspacePath, "skills", name, "SKILL.md")); err != nil {
			missing = true
			break
		}
	}
	refreshDue := lastSync.IsZero() || time.Since(lastSync) >= time.Duration(source.RefreshHours)*time.Hour
	if !missing && !refreshDue {
		return false, nil
	}
	if err := noderuntime.EnsureDefault(ctx); err != nil {
		return false, err
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		return false, fmt.Errorf("external skills %q require npx: %w", source.ID, err)
	}
	args := []string{"--yes", "skills", "add", source.Source, "--agent", "universal", "--copy", "--full-depth", "-y"}
	for _, name := range source.Install {
		args = append(args, "--skill", name)
	}
	if output, err := run(ctx, workspacePath, npx, args); err != nil {
		return false, fmt.Errorf("sync external skills from %q: %w\n%s", source.Source, err, output)
	}
	if err := mirrorCLISkills(workspacePath, source.Install); err != nil {
		return false, fmt.Errorf("mirror external skills from %q: %w", source.Source, err)
	}
	for _, name := range source.Install {
		if _, err := os.Stat(filepath.Join(workspacePath, "skills", name, "SKILL.md")); err != nil {
			return false, fmt.Errorf("external source %q did not install required skill %q", source.Source, name)
		}
	}
	return true, nil
}

func verifyCLI(ctx context.Context, workspacePath string, dependency CLIDependency, lastSync time.Time) (bool, error) {
	if !lastSync.IsZero() && time.Since(lastSync) < time.Duration(dependency.RefreshHours)*time.Hour {
		return false, nil
	}
	if err := noderuntime.EnsureDefault(ctx); err != nil {
		return false, err
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		return false, fmt.Errorf("CLI dependency %q requires npx: %w", dependency.ID, err)
	}
	args := npxCLIArgs(dependency)
	output, err := run(ctx, workspacePath, npx, args)
	if err != nil {
		return false, fmt.Errorf("verify CLI dependency %q: %w\n%s", dependency.ID, err, output)
	}
	if err := validateRequiredJSONChecks(output, dependency.Verify.RequiredJSONChecks); err != nil {
		return false, fmt.Errorf("verify CLI dependency %q: %w", dependency.ID, err)
	}
	return true, nil
}

func npxCLIArgs(dependency CLIDependency) []string {
	packageName := dependency.Package.Name + "@" + dependency.Package.Version
	args := []string{"--yes", "--package", packageName, dependency.Execution.Binary}
	return append(args, dependency.Verify.Args...)
}

func validateRequiredJSONChecks(output string, required []string) error {
	if len(required) == 0 {
		return nil
	}
	var report struct {
		Checks []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"checks"`
	}
	jsonStart := strings.Index(output, "{")
	if jsonStart < 0 {
		return fmt.Errorf("expected JSON verification report")
	}
	if err := json.Unmarshal([]byte(output[jsonStart:]), &report); err != nil {
		return fmt.Errorf("expected JSON verification report: %w", err)
	}
	checks := make(map[string]bool, len(report.Checks))
	for _, check := range report.Checks {
		checks[check.Name] = check.OK
	}
	for _, name := range required {
		if !checks[name] {
			return fmt.Errorf("required check %q did not pass", name)
		}
	}
	return nil
}

func run(ctx context.Context, dir, binary string, args []string) (string, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "NO_COLOR=1")
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// LoadAttachedSkills turns only the configured core skills into native agent
// skill bundles. The rest remain ordinary files for progressive disclosure.
func LoadAttachedSkills(workspacePath string, manifest Manifest) ([]*llmtypes.Skill, error) {
	var attached []*llmtypes.Skill
	for _, source := range manifest.Skills {
		for _, name := range source.Attach {
			skill, err := loadLocalSkill(workspacePath, name, source.Source)
			if err != nil {
				return nil, err
			}
			attached = append(attached, skill)
		}
	}
	return attached, nil
}

// mirrorCLISkills copies the skills this source declared from the CLI's
// staging folder into the workspace's own skills/ folder.
//
// install scopes it deliberately. Mirroring every directory found under
// .agents/skills meant anything left there by an earlier run or a different
// source silently entered the product surface, and any same-named skill the
// workspace already owned was deleted and replaced by the external copy. A
// product's tool/skill surface is supposed to be what its manifest declared,
// not whatever happens to be sitting in a staging directory.
//
// Each skill is staged beside its destination and swapped in, so a copy that
// fails partway leaves the previous skill intact rather than destroying it
// with a RemoveAll that already happened.
func mirrorCLISkills(workspacePath string, install []string) error {
	sourceRoot := filepath.Join(workspacePath, ".agents", "skills")
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return fmt.Errorf("read npx skills output: %w", err)
	}
	wanted := make(map[string]struct{}, len(install))
	for _, name := range install {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			wanted[trimmed] = struct{}{}
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// An empty install list means the source named no skills; mirroring
		// everything in that case is the same over-broad copy, so mirror
		// nothing and let the caller's post-install check report it.
		if _, ok := wanted[entry.Name()]; !ok {
			continue
		}
		source := filepath.Join(sourceRoot, entry.Name())
		destination := filepath.Join(workspacePath, "skills", entry.Name())
		staged := destination + ".incoming"
		if err := os.RemoveAll(staged); err != nil {
			return err
		}
		if err := copySkillTree(source, staged); err != nil {
			os.RemoveAll(staged)
			return err
		}
		if err := os.RemoveAll(destination); err != nil {
			os.RemoveAll(staged)
			return err
		}
		if err := os.Rename(staged, destination); err != nil {
			os.RemoveAll(staged)
			return err
		}
	}
	return nil
}

func copySkillTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
}

func loadLocalSkill(workspacePath, name, source string) (*llmtypes.Skill, error) {
	root := filepath.Join(workspacePath, "skills", name)
	data, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("read external skill %q: %w", name, err)
	}
	frontmatter, content, err := workspaceskills.ParseSkillFile(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse external skill %q: %w", name, err)
	}
	skill := &llmtypes.Skill{
		Name:                   name,
		Description:            frontmatter.Description,
		Content:                content,
		DisableModelInvocation: frontmatter.DisableModelInvocation,
		Source:                 llmtypes.SkillSource{Origin: "imported", SourceURL: source},
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Base(path) == "SKILL.md" {
			return walkErr
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		skill.SupportingFiles = append(skill.SupportingFiles, llmtypes.SkillFile{RelPath: filepath.ToSlash(rel), Content: contents})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load external skill files %q: %w", name, err)
	}
	return skill, nil
}

func readState(workspacePath string) (dependencyState, error) {
	data, err := os.ReadFile(filepath.Join(workspacePath, statePath))
	if err != nil {
		if os.IsNotExist(err) {
			return dependencyState{Skills: map[string]time.Time{}, CLI: map[string]time.Time{}}, nil
		}
		return dependencyState{}, err
	}
	var state dependencyState
	if err := json.Unmarshal(data, &state); err != nil {
		return dependencyState{}, err
	}
	if state.Skills == nil {
		state.Skills = map[string]time.Time{}
	}
	if state.CLI == nil {
		state.CLI = map[string]time.Time{}
	}
	return state, nil
}

func writeState(workspacePath string, state dependencyState) error {
	destination := filepath.Join(workspacePath, statePath)
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(destination, append(data, '\n'), 0600)
}

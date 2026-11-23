package cursorcli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Cursor uses a private plugin for instructions/skills/hooks and its documented
// user config directory for CLI permissions. The ACP session keeps the real cwd.
// Account files stay in the already-selected private account home; links retain
// its authentication and native records without copying credentials to a project.
func cursorPrivateSourceConfig(env []string) (string, error) {
	values := map[string]string{}
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok {
			values[k] = v
		}
	}
	source := values["CURSOR_CONFIG_DIR"]
	if source == "" {
		source = filepath.Join(values["HOME"], ".cursor")
	}
	if values["CURSOR_CONFIG_DIR"] == "" && runtime.GOOS != "darwin" && values["HOME"] == os.Getenv("HOME") && values["XDG_CONFIG_HOME"] != "" {
		source = filepath.Join(values["XDG_CONFIG_HOME"], "cursor")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	return source, nil
}

// PrivateRuntimeConfigDirectory uses the same selected account environment as
// launch, so a native importer never guesses another account's storage path.
func PrivateRuntimeConfigDirectory(runtimeDir string, env []string) (string, error) {
	source, err := cursorPrivateSourceConfig(env)
	if err != nil {
		return "", err
	}
	accountKey := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	return filepath.Join(runtimeDir, "cursor-accounts", accountKey), nil
}

// NativeRuntimeConfigDirectory follows only the link our launcher creates to
// this exact account's ordinary chat directory. Other links are refused.
func NativeRuntimeConfigDirectory(runtimeDir string, env []string) (string, error) {
	config, err := PrivateRuntimeConfigDirectory(runtimeDir, env)
	if err != nil {
		return "", err
	}
	link := filepath.Join(config, "chats")
	info, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return config, nil
	}
	source, err := cursorPrivateSourceConfig(env)
	if err != nil {
		return "", err
	}
	expected := filepath.Join(source, "chats")
	target, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(config, target)
	}
	if filepath.Clean(target) != filepath.Clean(expected) {
		return "", fmt.Errorf("native Cursor chats point outside the selected account")
	}
	info, err = os.Lstat(expected)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("native Cursor account chats contain a link")
	}
	return source, nil
}

func prepareCursorPrivateRuntime(opts *llmtypes.CallOptions, env []string) (string, error) {
	runtimeDir := llmtypes.CLIArtifactDirectory(opts, "")
	source, err := cursorPrivateSourceConfig(env)
	if err != nil {
		return "", err
	}
	// Native records created by Cursor also belong to the selected account.
	// Keep each account's generated config separate, including newly-created
	// stores that have no corresponding link in its account home yet.
	configDir, err := PrivateRuntimeConfigDirectory(runtimeDir, env)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}
	config := map[string]any{"version": 1, "editor": map[string]any{"vimMode": false}}
	if raw, err := os.ReadFile(filepath.Join(source, "cli-config.json")); err == nil {
		if err = json.Unmarshal(raw, &config); err != nil {
			return "", fmt.Errorf("read Cursor account configuration: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if config == nil {
		return "", fmt.Errorf("Cursor account configuration must be an object")
	}
	sourceEntries, err := os.ReadDir(source)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	desired := map[string]bool{}
	for _, entry := range sourceEntries {
		if entry.Name() != "cli-config.json" && entry.Name() != "hooks.json" {
			desired[entry.Name()] = true
		}
	}
	// Keep existing selected-account links in place while another chat starts.
	// Only links whose source disappeared are removed; targets are never removed.
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Name() == "cli-config.json" || desired[entry.Name()] {
			continue
		}
		target := filepath.Join(configDir, entry.Name())
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
	}
	for _, entry := range sourceEntries {
		if !desired[entry.Name()] {
			continue
		}
		target := filepath.Join(configDir, entry.Name())
		info, err := os.Lstat(target)
		if err == nil && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		from := filepath.Join(source, entry.Name())
		if link, err := os.Readlink(target); err == nil && link == from {
			continue
		}
		if err := replaceCursorAccountLink(from, target); err != nil {
			return "", err
		}
	}
	raw, err := os.ReadFile(filepath.Join(runtimeDir, ".cursor", "cli.json"))
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read generated Cursor policy: %w", err)
	}
	generated := map[string]any{}
	if err == nil {
		if err = json.Unmarshal(raw, &generated); err != nil {
			return "", err
		}
	}
	permissions, _ := config["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
	}
	if projected, ok := generated["permissions"].(map[string]any); ok {
		for _, key := range []string{"allow", "deny"} {
			prior, _ := permissions[key].([]any)
			additions, _ := projected[key].([]any)
			permissions[key] = append(prior, additions...)
		}
	}
	config["permissions"] = permissions
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	if err = writeCursorPrivateFile(filepath.Join(configDir, "cli-config.json"), encoded); err != nil {
		return "", err
	}
	manifest := map[string]any{"name": "agentworks-runtime", "rules": ".cursor/rules", "skills": ".cursor/skills"}
	hooksPath := filepath.Join(runtimeDir, ".cursor", "hooks.json")
	if raw, err := os.ReadFile(hooksPath); err == nil {
		// Existing hook commands are project-relative. Plugin hooks must point to
		// their private scripts when the actual session cwd is the person's folder.
		var hooks map[string]any
		if err = json.Unmarshal(raw, &hooks); err != nil {
			return "", err
		}
		if groups, ok := hooks["hooks"].(map[string]any); ok {
			for _, group := range groups {
				if entries, ok := group.([]any); ok {
					for _, entry := range entries {
						if hook, ok := entry.(map[string]any); ok {
							if command, ok := hook["command"].(string); ok && strings.HasPrefix(command, "./.cursor/") {
								hook["command"] = filepath.Join(runtimeDir, strings.TrimPrefix(command, "./"))
							}
						}
					}
				}
			}
		}
		encoded, err = json.Marshal(hooks)
		if err != nil {
			return "", err
		}
		if err = writeCursorPrivateFile(hooksPath, encoded); err != nil {
			return "", err
		}
		manifest["hooks"] = ".cursor/hooks.json"
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err = os.MkdirAll(filepath.Join(runtimeDir, ".cursor-plugin"), 0700); err != nil {
		return "", err
	}
	encoded, err = json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err = writeCursorPrivateFile(filepath.Join(runtimeDir, ".cursor-plugin", "plugin.json"), encoded); err != nil {
		return "", err
	}
	// Transcript readers follow the active account, while old records remain
	// in that account's directory and cannot be reused by the next account.
	current := filepath.Join(runtimeDir, "cursor-config")
	if info, err := os.Lstat(current); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("private Cursor config pointer must be a link")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if target, err := os.Readlink(current); err != nil || target != configDir {
		if err := replaceCursorAccountLink(configDir, current); err != nil {
			return "", err
		}
	}
	return configDir, nil
}

// A temp directory is ignored by other launchers' symlink cleanup. Rename
// publishes a complete link without making an existing login path disappear.
func replaceCursorAccountLink(source, target string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(target), ".account-link-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	link := filepath.Join(tmp, "entry")
	if err := os.Symlink(source, link); err != nil {
		return err
	}
	return os.Rename(link, target)
}

// A parallel launch must see a complete policy/hook document, never a file
// another launch has truncated but not written yet.
func writeCursorPrivateFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".cursor-config-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

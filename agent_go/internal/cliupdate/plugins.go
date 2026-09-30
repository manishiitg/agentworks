package cliupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Native MCP ships with Pi. No third-party MCP extension is managed or refreshed.
// Retain the generic updater for future platform extensions.
var managedPiTempPlugins = []string{}

const piNpmViewTimeout = 10 * time.Second

// updatePiTempPlugins refreshes managed plugins in pi's temporary npm
// extension cache. It mirrors pi's own install mechanics (verified against
// pi-coding-agent 0.86.1): cache root <agentDir>/tmp/extensions/npm/<hash>,
// `npm view <name> version --json` for the latest version, and
// `npm install <name>@latest --prefix <root> --legacy-peer-deps` for the
// upgrade. A missing cache is not an error: pi installs on next launch.
func updatePiTempPlugins(ctx context.Context, env []string) error {
	agentDir, err := piAgentDir()
	if err != nil {
		return err
	}
	root := piTempNpmRoot(agentDir)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	installed := make(map[string]string, len(managedPiTempPlugins))
	for _, name := range managedPiTempPlugins {
		version, err := piInstalledPluginVersion(root, name)
		if err != nil {
			return err
		}
		if version != "" {
			installed[name] = version
		}
	}
	if len(installed) == 0 {
		return nil
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("npm not found: %w", err)
	}
	for name, current := range installed {
		latest, err := piLatestPluginVersion(ctx, env, root, npm, name)
		if err != nil {
			return err
		}
		if !npmVersionNewer(latest, current) {
			continue
		}
		if _, err := run(ctx, root, env, npm, "install", name+"@latest", "--prefix", root, "--legacy-peer-deps"); err != nil {
			return fmt.Errorf("install %s: %w", name, err)
		}
		after, err := piInstalledPluginVersion(root, name)
		if err != nil {
			return err
		}
		if after == "" || after == current {
			return fmt.Errorf("%s still at %s after update", name, current)
		}
	}
	return nil
}

// piAgentDir mirrors pi's agent directory resolution: PI_CODING_AGENT_DIR
// when set, otherwise ~/.pi/agent.
func piAgentDir() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); configured != "" {
		if configured == "~" {
			return os.UserHomeDir()
		}
		if strings.HasPrefix(configured, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(home, configured[2:]), nil
		}
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// piTempNpmRoot mirrors pi's getTemporaryDir("npm") for spec-less installs:
// <agentDir>/tmp/extensions/npm/<sha256("npm-")[:8]>.
func piTempNpmRoot(agentDir string) string {
	sum := sha256.Sum256([]byte("npm-"))
	return filepath.Join(agentDir, "tmp", "extensions", "npm", hex.EncodeToString(sum[:])[:8])
}

// piInstalledPluginVersion reads the installed version of a cached plugin.
// It returns "" when the plugin is not cached (pi installs on next launch).
func piInstalledPluginVersion(root, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "node_modules", name, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("parse %s package.json: %w", name, err)
	}
	return strings.TrimSpace(manifest.Version), nil
}

func piLatestPluginVersion(ctx context.Context, env []string, dir, npm, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, piNpmViewTimeout)
	defer cancel()
	out, err := run(ctx, dir, env, npm, "view", name, "version", "--json")
	if err != nil {
		return "", fmt.Errorf("npm view %s: %w", name, err)
	}
	return parseNpmViewVersion(strings.TrimSpace(string(out)))
}

// parseNpmViewVersion handles `npm view <name> version --json`, which
// returns a JSON string, or an array of strings when several versions match.
func parseNpmViewVersion(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty response from npm view")
	}
	var single string
	if err := json.Unmarshal([]byte(raw), &single); err == nil && single != "" {
		return single, nil
	}
	var multiple []string
	if err := json.Unmarshal([]byte(raw), &multiple); err != nil {
		return "", fmt.Errorf("unexpected response from npm view: %s", raw)
	}
	latest := ""
	for _, v := range multiple {
		if v != "" && (latest == "" || npmVersionNewer(v, latest)) {
			latest = v
		}
	}
	if latest == "" {
		return "", fmt.Errorf("unexpected response from npm view: %s", raw)
	}
	return latest, nil
}

// npmVersionNewer reports whether latest is strictly newer than installed.
// Both are expected to be semver; unparseable input fails closed to false so
// a surprising registry response never triggers an install loop.
func npmVersionNewer(latest, installed string) bool {
	l, ok := parseNpmVersion(latest)
	if !ok {
		return false
	}
	i, ok := parseNpmVersion(installed)
	if !ok {
		return false
	}
	for n := 0; n < len(l.core) || n < len(i.core); n++ {
		var ln, in int
		if n < len(l.core) {
			ln = l.core[n]
		}
		if n < len(i.core) {
			in = i.core[n]
		}
		if ln != in {
			return ln > in
		}
	}
	if l.pre == i.pre {
		return false
	}
	if l.pre == "" {
		return true
	}
	if i.pre == "" {
		return false
	}
	return l.pre > i.pre
}

type npmVersion struct {
	core []int
	pre  string
}

func parseNpmVersion(s string) (npmVersion, bool) {
	var v npmVersion
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	core, pre, _ := strings.Cut(s, "-")
	core, _, _ = strings.Cut(core, "+")
	pre, _, _ = strings.Cut(pre, "+")
	if core == "" {
		return npmVersion{}, false
	}
	for _, part := range strings.Split(core, ".") {
		if part == "" {
			return npmVersion{}, false
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return npmVersion{}, false
			}
			n = n*10 + int(r-'0')
		}
		v.core = append(v.core, n)
	}
	v.pre = pre
	return v, len(v.core) > 0
}

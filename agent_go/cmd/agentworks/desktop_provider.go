package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Desktop apps carry native command lines; using them needs neither npm nor a
// separately installed CLI. Only existing regular executable files qualify.
func desktopExecutable(patterns []string, goos string) string {
	var found string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || (goos != "windows" && info.Mode()&0111 == 0) {
				continue
			}
			if found == "" || versionNewer(desktopCLIVersion(path), desktopCLIVersion(found)) {
				found = path
			}
		}
		if found != "" {
			return found
		}
	}
	return ""
}

func desktopCLIVersion(path string) string {
	// Claude downloads versioned Code runtimes independently of its app bundle.
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if len(part) > 0 && part[0] >= '0' && part[0] <= '9' && strings.Count(part, ".") >= 2 {
			return part
		}
	}
	return ""
}

func codexDesktopPatterns() []string {
	if codexGOOS == "darwin" {
		home, _ := os.UserHomeDir()
		return []string{codexAppBundleCLI,
			"/Applications/Codex.app/Contents/Resources/codex",
			filepath.Join(home, "Applications", "Codex.app", "Contents", "Resources", "codex")}
	}
	if codexGOOS != "windows" {
		return nil
	}
	var patterns []string
	for _, root := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles")} {
		if root == "" {
			continue
		}
		for _, app := range []string{"Codex", "ChatGPT", filepath.Join("Programs", "Codex"), filepath.Join("Programs", "ChatGPT"), filepath.Join("WindowsApps", "OpenAI.Codex_*"), filepath.Join("WindowsApps", "OpenAI.ChatGPT-Desktop_*")} {
			patterns = append(patterns, codexWindowsPatterns(filepath.Join(root, app))...)
		}
	}
	// Appx resolves the current user's Store installation even when WindowsApps
	// itself cannot be enumerated. No elevated access or package changes.
	for _, root := range windowsCodexPackageRoots() {
		patterns = append(patterns, codexWindowsPatterns(root)...)
	}
	return patterns
}

func codexWindowsPatterns(root string) []string {
	return []string{filepath.Join(root, "app", "resources", "codex.exe"),
		filepath.Join(root, "resources", "codex.exe"),
		filepath.Join(root, "app", "resources", "codex-cli", "bin", "codex.exe"),
		filepath.Join(root, "resources", "codex-cli", "bin", "codex.exe")}
}

func claudeExecutable(onPath func(string) bool) string {
	if onPath("claude") {
		return "claude"
	}
	var patterns []string
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		patterns = []string{filepath.Join(home, "Library", "Application Support", "Claude", "claude-code", "*", "claude.app", "Contents", "MacOS", "claude")}
	} else if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				patterns = append(patterns, filepath.Join(root, "Claude", "claude-code", "*", "claude.exe"))
			}
		}
	}
	return desktopExecutable(patterns, runtime.GOOS)
}

func pluginSetupError(provider string, err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail != "" {
		return fmt.Errorf("%s plugin setup failed: %w\n%s", provider, err, detail)
	}
	if provider == "claude" && (os.IsNotExist(err) || isExecutableStartError(err)) {
		return fmt.Errorf("could not start Claude Code: %w. Open the Claude desktop app's Code tab once, then run agentworks setup claude again. You can also add manishiitg/agentworks-plugins in Claude's plugin settings", err)
	}
	return fmt.Errorf("%s plugin setup failed: %w", provider, err)
}

func isExecutableStartError(err error) bool {
	_, ok := err.(*exec.Error)
	if ok {
		return true
	}
	_, ok = err.(*os.PathError)
	return ok
}

func runPluginSteps(ctx context.Context, o *options, provider string, steps [][]string) error {
	for _, args := range steps {
		c := exec.CommandContext(ctx, providerExecutable(provider), args...)
		c.Stdin = o.stdin
		out, err := c.CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(out)), "already") {
			return pluginSetupError(provider, err, out)
		}
		fmt.Fprint(o.stderr, string(out))
	}
	return nil
}

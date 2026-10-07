package cliupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var errCLINotInstalled = errors.New("CLI not installed")

// updateGlobalCLI invokes the updater supplied by the user's installed CLI.
// It never downloads or selects an AgentWorks-owned copy.
func updateGlobalCLI(ctx context.Context, p Provider, _ string, _ Result) (string, string, error) {
	executable, err := findGlobalCLI(p)
	if err != nil {
		return "", "", err
	}
	args, ok := map[string][]string{
		"codex":        {"update"},
		"claude":       {"update"},
		"cursor-agent": {"update"},
		"pi":           {"update", "--self"},
	}[p.Name]
	if !ok {
		return "", "", fmt.Errorf("no global update command for %s", p.Name)
	}
	env := append(updateEnv(), npmPrefixEnv(executable)...)
	if _, err := run(ctx, updateWorkingDir(executable), env, executable, args...); err != nil {
		return "", "", fmt.Errorf("update %s: %w", p.Name, err)
	}

	// Self-updaters commonly replace a symlink or launcher, so discover it
	// again instead of retaining the pre-update path.
	executable, err = findGlobalCLI(p)
	if err != nil {
		return "", "", fmt.Errorf("find updated %s: %w", p.Name, err)
	}
	version, err := smoke(ctx, executable, env, p)
	if err != nil {
		return "", "", fmt.Errorf("verify updated %s: %w", p.Name, err)
	}
	if p.Name == "pi" {
		// `pi update --self` covers the binary only, and pi never refreshes
		// temporary `-e npm:` extensions on its own, so managed plugins are
		// refreshed here on the same cadence and retry policy.
		if err := updatePiTempPlugins(ctx, env); err != nil {
			return "", "", fmt.Errorf("update pi plugins: %w", err)
		}
	}
	return executable, version, nil
}

func findGlobalCLI(p Provider) (string, error) {
	if p.Name == "pi" {
		if configured := strings.TrimSpace(os.Getenv("PI_BIN")); configured != "" {
			if !filepath.IsAbs(configured) {
				return "", errors.New("PI_BIN must be absolute")
			}
			if info, err := os.Stat(configured); err != nil || info.IsDir() || info.Mode()&0111 == 0 {
				return "", errCLINotInstalled
			}
			return filepath.Clean(configured), nil
		}
	}
	names := []string{p.Name}
	if p.Name == "cursor-agent" {
		names = append(names, "agent")
	}
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.Clean(absolute), nil
	}
	return "", errCLINotInstalled
}

// npmPrefixEnv keeps an npm-installed CLI's self-update ("codex update" runs
// npm install -g) in the prefix the CLI was installed into. Without it npm
// used its own default prefix, Node's folder: on Citymall that put a second
// codex in tools/node/bin, ahead of the managed tools/bin on PATH, and the
// next deploy refused it ("codex resolves outside the managed installation").
func npmPrefixEnv(executable string) []string {
	dir := filepath.Dir(executable)
	if filepath.Base(dir) != "bin" {
		return nil
	}
	prefix := filepath.Dir(dir)
	if info, err := os.Stat(filepath.Join(prefix, "lib", "node_modules")); err != nil || !info.IsDir() {
		return nil
	}
	return []string{"npm_config_prefix=" + prefix}
}

func updateWorkingDir(executable string) string {
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		return home
	}
	return filepath.Dir(executable)
}

func smoke(ctx context.Context, binary string, env []string, p Provider) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"--version"}
	if p.Name == "cursor-agent" {
		args = append([]string{"--disable-auto-update"}, args...)
	}
	out, err := run(ctx, updateWorkingDir(binary), env, binary, args...)
	if err != nil {
		return "", err
	}
	version := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	if version == "" || len(version) > 200 {
		return "", errors.New("invalid --version response")
	}
	return version, nil
}

// Updaters need the user's real installation/config directories, but they do
// not receive workflow/provider secrets. Proxy and CA settings are retained.
func updateEnv() []string {
	allowed := map[string]bool{
		"HOME": true, "USER": true, "LOGNAME": true, "PATH": true,
		"SHELL": true, "TMPDIR": true, "XDG_CONFIG_HOME": true,
		"XDG_DATA_HOME": true, "XDG_CACHE_HOME": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "no_proxy": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true,
	}
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if allowed[key] || key == "LANG" || strings.HasPrefix(key, "LC_") {
			env = append(env, entry)
		}
	}
	return env
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if left := 64*1024 - b.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func run(ctx context.Context, dir string, env []string, executable string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Update output can contain credentials embedded in proxy URLs.
		return nil, fmt.Errorf("%s: %w", filepath.Base(executable), err)
	}
	return out.Bytes(), nil
}

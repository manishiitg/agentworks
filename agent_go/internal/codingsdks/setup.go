// Package codingsdks prepares native transport dependencies outside chat folders.
package codingsdks

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed coding-sdks.cjs
var installer []byte

type setup struct {
	done  chan struct{}
	err   error
	entry string
}

type Manager struct {
	root, script string
	ctx          context.Context
	log          func(string, ...any)
	allowInstall bool
	mu           sync.Mutex
	runs         map[string]*setup
}

func New(ctx context.Context, root string, allowInstall bool, log func(string, ...any)) (*Manager, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("AI component destination must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	script := filepath.Join(root, "coding-sdks.cjs")
	if err := os.WriteFile(script, installer, 0600); err != nil {
		return nil, err
	}
	return &Manager{root: root, script: script, ctx: ctx, allowInstall: allowInstall, log: log, runs: map[string]*setup{}}, nil
}

func (m *Manager) Start() {
	for _, provider := range []string{"claude-code", "cursor-cli"} {
		go func() { _ = m.Ensure(m.ctx, provider) }()
	}
}

// Ensure shares an in-flight setup, retries a failed setup on the next request,
// and lets a cancelled chat stop waiting without cancelling app provisioning.
func (m *Manager) Ensure(ctx context.Context, provider string) error {
	if provider != "claude-code" && provider != "cursor-cli" {
		return nil
	}
	m.mu.Lock()
	run := m.runs[provider]
	if run != nil {
		select {
		case <-run.done:
			if run.err != nil {
				run = nil
			} else if _, err := os.Stat(run.entry); err != nil {
				run = nil
			}
		default:
		}
	}
	if run == nil {
		run = &setup{done: make(chan struct{})}
		m.runs[provider] = run
		go m.prepare(provider, run)
	}
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-run.done:
		return run.err
	}
}

func (m *Manager) prepare(provider string, run *setup) {
	defer close(run.done)
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
	defer cancel()
	node, err := exec.LookPath("node")
	if err != nil {
		run.err = errors.New("Node.js and npm are needed. Install Node.js, then open AgentWorks again.")
		m.report("[CODING SDK] %s: %v", provider, run.err)
		return
	}
	// Separate npm prefixes let either provider finish without waiting on the other.
	args := []string{m.script, "--prefix", filepath.Join(m.root, provider), "--provider", provider, "--prefer-global"}
	if !m.allowInstall {
		args = append(args, "--check-only")
	}
	cmd := exec.CommandContext(ctx, node, args...)
	cmd.Env = environment()
	cmd.WaitDelay = 5 * time.Second
	output, commandErr := cmd.Output()
	var result struct {
		Entries map[string]string `json:"entries"`
		Errors  []string          `json:"errors"`
		Node    string            `json:"node"`
		NPM     string            `json:"npm"`
	}
	variable := "CLAUDE_AGENT_SDK_PATH"
	label := "Claude"
	if provider == "cursor-cli" {
		variable, label = "CURSOR_SDK_PATH", "Cursor"
	}
	if json.Unmarshal(output, &result) != nil {
		run.err = fmt.Errorf("%s needs a component that could not be prepared. Update the app and try again", label)
	} else if len(result.Errors) > 0 {
		run.err = errors.New(strings.Join(result.Errors, " "))
	} else if commandErr != nil || result.Entries[variable] == "" {
		run.err = fmt.Errorf("%s setup could not finish. Check your internet connection and try again", label)
	} else {
		run.entry = result.Entries[variable]
		run.err = os.Setenv(variable, run.entry)
	}
	m.report("[CODING SDK] provider=%s node=%q npm=%q ready=%t", provider, result.Node, result.NPM, run.err == nil)
	if run.err != nil {
		m.report("[CODING SDK] %s: %v", provider, run.err)
	}
}

func (m *Manager) report(format string, args ...any) {
	if m.log != nil {
		m.log(format, args...)
	}
}

// Installation receives normal runtime/proxy paths, never model or workflow secrets.
func environment() []string {
	allowed := map[string]bool{}
	for _, key := range strings.Fields("HOME USER LOGNAME PATH SHELL TMPDIR TEMP TMP USERPROFILE APPDATA LOCALAPPDATA SYSTEMROOT WINDIR COMSPEC PATHEXT PROGRAMFILES PROGRAMFILES(X86) XDG_CONFIG_HOME XDG_DATA_HOME XDG_CACHE_HOME HTTP_PROXY HTTPS_PROXY NO_PROXY SSL_CERT_FILE SSL_CERT_DIR NODE_EXTRA_CA_CERTS CLAUDE_AGENT_SDK_PATH CURSOR_SDK_PATH") {
		allowed[key] = true
	}
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if allowed[upper] || upper == "LANG" || strings.HasPrefix(upper, "LC_") {
			env = append(env, item)
		}
	}
	return env
}

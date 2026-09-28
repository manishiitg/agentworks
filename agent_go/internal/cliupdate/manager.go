// Package cliupdate keeps the user's installed coding CLIs current across
// backend restarts. AgentWorks does not own a parallel CLI installation.
package cliupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const Interval = 24 * time.Hour
const RetryInterval = time.Hour

type Provider struct{ Name string }

var providers = []Provider{
	{"codex"},
	{"claude"},
	{"pi"},
	{"cursor-agent"},
	{"muse"},
}

// selfUpdatingProviders never get an explicit update command: their own launcher
// already checks for and applies updates in the background on its own
// cadence (muse's ~/.local/bin/muse wrapper polls MUSE_CHANNEL_URL every
// MUSE_UPDATE_INTERVAL_SECONDS, default 1h, with no invocation needed).
// codex/claude/cursor-agent/pi all expose an `update` subcommand too, and
// this package's scheduled Check invokes it against the global executable.
// Muse is the only one where an explicit update would just race an
// update already happening on its own.
var selfUpdatingProviders = map[string]bool{"muse": true}

type Result struct {
	Status      string    `json:"status"`
	Version     string    `json:"version,omitempty"`
	Executable  string    `json:"executable,omitempty"`
	LastAttempt time.Time `json:"last_attempt,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	NextCheck   time.Time `json:"next_check_at"`
	Error       string    `json:"error,omitempty"`
}
type State struct {
	SchemaVersion int               `json:"schema_version"`
	LastAttempt   time.Time         `json:"last_attempt,omitempty"`
	LastSuccess   time.Time         `json:"last_success,omitempty"`
	NextCheck     time.Time         `json:"next_check_at"`
	CLIs          map[string]Result `json:"clis"`
}

type Installer func(context.Context, Provider, string, Result) (executable, version string, err error)
type Manager struct {
	Root    string
	Install Installer
	Now     func() time.Time
	Log     func(string, ...any)
}

func DefaultRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")); root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("AGENTWORKS_STATE_ROOT must be absolute")
		}
		return filepath.Join(root, "cli-updates"), nil
	}
	root, err := os.UserConfigDir()
	return filepath.Join(root, "agentworks", "cli-updates"), err
}

func New(root string, log func(string, ...any)) (*Manager, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("CLI update root must be absolute")
	}
	m := &Manager{Root: root, Now: time.Now, Log: log, Install: updateGlobalCLI}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	m.Root = canonical
	return m, nil
}

// Prepare runs once before agents start. It deliberately creates no shims:
// every launch must resolve the same user-installed executable.
func (m *Manager) Prepare() error {
	return nil
}

// Activate removes the retired managed-shim override from an inherited
// environment. It never changes the user's shell or prepends a private PATH.
func (m *Manager) Activate() error {
	if err := os.Unsetenv("AGENTWORKS_MANAGED_CLI_BIN"); err != nil {
		return err
	}
	retiredBin := filepath.Clean(filepath.Join(m.Root, "bin"))
	parts := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	kept := parts[:0]
	for _, part := range parts {
		if filepath.Clean(part) != retiredBin {
			kept = append(kept, part)
		}
	}
	return os.Setenv("PATH", strings.Join(kept, string(os.PathListSeparator)))
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(RetryInterval)
	defer ticker.Stop()
	for {
		if err := m.Check(ctx); err != nil && ctx.Err() == nil && m.Log != nil {
			m.Log("[CLI UPDATE] %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Check coordinates all backend processes sharing a state root. flock is
// released by the OS on crashes, unlike a mkdir lock that can remain forever.
func (m *Manager) Check(ctx context.Context) error {
	lock, err := os.OpenFile(filepath.Join(m.Root, "update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	s, err := m.ReadState()
	if err != nil {
		return err
	}
	changed := false
	for _, p := range providers {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := m.Now().UTC()
		r := s.CLIs[p.Name]
		if now.Before(r.NextCheck) && !recordedExecutableMissing(r) {
			continue
		}
		changed = true
		s.LastAttempt, r.LastAttempt = now, now
		if selfUpdatingProviders[p.Name] {
			version, executable, probeErr := probeSelfUpdatingVersion(ctx, p.Name)
			if probeErr != nil {
				r.Status, r.Error, r.NextCheck = "not_installed", "", now.Add(Interval)
			} else {
				r.Status, r.Error, r.Version, r.Executable, r.LastSuccess, r.NextCheck =
					"self_updating", "", version, executable, now, now.Add(RetryInterval)
			}
			s.CLIs[p.Name] = r
			if err := m.save(&s); err != nil {
				return err
			}
			if m.Log != nil {
				m.Log("[CLI UPDATE] %s: %s (version=%s)", p.Name, r.Status, r.Version)
			}
			continue
		}
		r.LastAttempt, r.NextCheck, r.Status, r.Error = now, now.Add(RetryInterval), "checking", ""
		s.LastAttempt, s.CLIs[p.Name] = now, r
		// Persist before starting: a crash is a failed attempt with a bounded retry.
		if err := m.save(&s); err != nil {
			return err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		executable, version, installErr := m.Install(attemptCtx, p, "", r)
		cancel()
		finished := m.Now().UTC()
		if errors.Is(installErr, errCLINotInstalled) {
			r.Status, r.Error, r.NextCheck = "not_installed", "", finished.Add(Interval)
			s.CLIs[p.Name] = r
			if err := m.save(&s); err != nil {
				return err
			}
			continue
		}
		if installErr == nil && (!filepath.IsAbs(executable) || version == "") {
			installErr = errors.New("updater returned no verified global executable")
		}
		if installErr != nil {
			r.Status, r.Error, r.NextCheck = "failed", installErr.Error(), finished.Add(RetryInterval)
		} else {
			r.Status = "updated"
			if r.Version == version {
				r.Status = "current"
			}
			r.Version, r.Executable, r.LastSuccess, r.NextCheck = version, executable, finished, finished.Add(Interval)
			// One installed release per CLI: drop the ones its updater left behind.
			if removed, pruneErr := pruneOldVersions(ctx, p.Name, executable); m.Log != nil && (len(removed) > 0 || pruneErr != nil) {
				m.Log("[CLI UPDATE] %s: removed old releases %v (err=%v)", p.Name, removed, pruneErr)
			}
		}
		s.CLIs[p.Name] = r
		if err := m.save(&s); err != nil {
			return err
		}
		if m.Log != nil {
			m.Log("[CLI UPDATE] %s: %s (version=%s)", p.Name, r.Status, r.Version)
		}
	}
	if changed {
		allSuccessful := len(s.CLIs) == len(providers)
		for _, r := range s.CLIs {
			if r.Status == "failed" || r.Status == "checking" {
				allSuccessful = false
			}
		}
		if allSuccessful {
			s.LastSuccess = m.Now().UTC()
		}
		if err := m.save(&s); err != nil {
			return err
		}
	}
	return m.removeRetiredPrivateInstallations()
}

// removeRetiredPrivateInstallations runs under update.lock after global update
// attempts finish. These directories were created exclusively by the old
// private-release manager and are no longer launch inputs.
func (m *Manager) removeRetiredPrivateInstallations() error {
	for _, name := range []string{"bin", "current", "releases", "sessions"} {
		if err := os.RemoveAll(filepath.Join(m.Root, name)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ReadState() (State, error) {
	s := State{SchemaVersion: 1, CLIs: make(map[string]Result)}
	data, err := os.ReadFile(filepath.Join(m.Root, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("invalid CLI update state (preserved): %w", err)
	}
	if s.SchemaVersion != 1 || s.CLIs == nil {
		return s, errors.New("unsupported CLI update state (preserved)")
	}
	return s, nil
}

func (m *Manager) save(s *State) error {
	s.NextCheck = time.Time{}
	for _, r := range s.CLIs {
		if s.NextCheck.IsZero() || r.NextCheck.Before(s.NextCheck) {
			s.NextCheck = r.NextCheck
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(m.Root, "state.json"), append(data, '\n'), 0600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// recordedExecutableMissing reports a CLI whose last good install no longer
// resolves. A later npm run in the same prefix can drop another package's bin
// link; the services then fall back to an older system copy until the next
// daily check (RTS 2026-09-25: ~/.local/bin/claude vanished after an update,
// and a root-installed 2.1.233 ran instead). Such a CLI is checked right away.
func recordedExecutableMissing(r Result) bool {
	if r.Executable == "" || r.Status == "not_installed" {
		return false
	}
	_, err := os.Stat(r.Executable)
	return err != nil
}

// probeSelfUpdatingVersion reports a self-updating provider's current
// version without touching PATH, symlinks, or the provider's own update
// cycle: this package never invokes an updater for these providers (see
// selfUpdatingProviders), it only observes what their own updater already
// produced. executable is the resolved binary path so State.CLIs carries
// the same field shape for every provider.
func probeSelfUpdatingVersion(ctx context.Context, name string) (version, executable string, err error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--version").Output()
	if err != nil {
		return "", "", fmt.Errorf("%s --version: %w", name, err)
	}
	firstLine := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	if firstLine == "" {
		return "", "", fmt.Errorf("%s --version produced no output", name)
	}
	return firstLine, path, nil
}

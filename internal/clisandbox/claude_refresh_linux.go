//go:build linux

package clisandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"golang.org/x/sys/unix"
)

// Only the trusted host copies refreshed credentials, never an agent shell.
// Watch the parent directory: Claude's atomic rename replaces the watched inode.
// Keep the account inode: other running Landlock CLIs hold a grant on that inode.
var claudeRefreshMu sync.Mutex
var claudeRefreshes = map[*claudeRefresh]bool{}

type claudeRefresh struct {
	src, dst, id string
	baseline     [32]byte
	watch        *fsnotify.Watcher
	done         chan struct{}
}

func credentialBytes(path string, write bool) (*os.File, []byte, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if write {
		flags = unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, nil, errors.New("credential file unavailable")
	}
	f := os.NewFile(uintptr(fd), "credential")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("credential is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		f.Close()
		return nil, nil, errors.New("credential file unreadable")
	}
	return f, data, nil
}

func validClaudeRefresh(data []byte) bool {
	var doc struct {
		OAuth struct {
			Access  string `json:"accessToken"`
			Refresh string `json:"refreshToken"`
			Expires int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	return json.Unmarshal(data, &doc) == nil && doc.OAuth.Access != "" && doc.OAuth.Refresh != "" && doc.OAuth.Expires > 0
}

func (s *claudeRefresh) record(action, reason string) {
	log.Printf("[CLAUDE_CREDENTIAL_REFRESH] provider=claude-code credential=%s action=%s reason=%s", s.id, action, reason)
}

// Caller holds claudeRefreshMu. A separately reconnected account wins over an
// old runtime; an authentication failure must never clear the account's login.
func (s *claudeRefresh) flush() {
	info, err := os.Lstat(s.dst)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	candidate, data, err := credentialBytes(s.dst, false)
	if err != nil {
		s.record("failed", "runtime_unreadable")
		return
	}
	candidate.Close()
	if !validClaudeRefresh(data) {
		s.record("skipped", "invalid_refresh")
		return
	}
	account, current, err := credentialBytes(s.src, true)
	if err != nil {
		s.record("failed", "account_unavailable")
		return
	}
	defer account.Close()
	if !bytes.Equal(data, current) {
		if sha256.Sum256(current) != s.baseline {
			s.record("skipped", "account_changed")
			return
		}
		if _, err = account.Seek(0, io.SeekStart); err == nil {
			_, err = account.Write(data)
		}
		if err == nil {
			err = account.Truncate(int64(len(data)))
		}
		if err == nil {
			err = account.Sync()
		}
		if err != nil {
			s.record("failed", "account_write")
			return
		}
		s.baseline = sha256.Sum256(data)
		for active := range claudeRefreshes {
			if active.src == s.src {
				active.baseline = s.baseline
			}
		}
		s.record("saved", "atomic_replacement")
	}
	if err = os.Remove(s.dst); err == nil {
		err = os.Symlink(s.src, s.dst)
	}
	if err != nil {
		s.record("failed", "relink")
		return
	}
	s.baseline = sha256.Sum256(data)
}

func flushClaudeCredentialSync(policy *llmtypes.CLISecurityPolicy) {
	if policy.Provider != "claude-code" {
		return
	}
	claudeRefreshMu.Lock()
	defer claudeRefreshMu.Unlock()
	// Flush before preparing another runtime too, even if its filesystem event
	// has not been handled yet. Never search directories or inspect another account.
	source := claudeCredentialSource(policy)
	for s := range claudeRefreshes {
		if s.src == source {
			s.flush()
		}
	}
}

func claudeCredentialSource(policy *llmtypes.CLISecurityPolicy) string {
	home := policy.CredentialHome
	env := os.Getenv
	if home == "" {
		home, _ = os.UserHomeDir()
	} else {
		env = func(k string) string { return policy.CredentialEnv[k] }
	}
	return credentialSource(credentialFiles["claude-code"][0], canonical(home), env)
}

func beginClaudeCredentialSync(policy *llmtypes.CLISecurityPolicy, home string) (func(), error) {
	noop := func() {}
	if policy.Provider != "claude-code" {
		return noop, nil
	}
	src := claudeCredentialSource(policy)
	if canonical(src) == filepath.Join(home, ".claude", ".credentials.json") {
		return noop, nil
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return noop, nil
	}
	claudeRefreshMu.Lock()
	defer claudeRefreshMu.Unlock()
	f, data, err := credentialBytes(src, false)
	if err != nil {
		return noop, err
	}
	f.Close()
	watch, err := fsnotify.NewWatcher()
	if err != nil {
		return noop, errors.New("cannot observe Claude credential refresh")
	}
	dst := filepath.Join(home, ".claude", ".credentials.json")
	if err = watch.Add(filepath.Dir(dst)); err != nil {
		watch.Close()
		return noop, errors.New("cannot observe Claude credential folder")
	}
	hash := sha256.Sum256([]byte(src))
	s := &claudeRefresh{src: src, dst: dst, id: hex.EncodeToString(hash[:8]), baseline: sha256.Sum256(data), watch: watch, done: make(chan struct{})}
	claudeRefreshes[s] = true
	s.record("started", "launch")
	go func() {
		defer close(s.done)
		for {
			select {
			case event, ok := <-watch.Events:
				if !ok {
					return
				}
				if event.Name == dst && event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) != 0 {
					claudeRefreshMu.Lock()
					s.flush()
					claudeRefreshMu.Unlock()
				}
			case _, ok := <-watch.Errors:
				if !ok {
					return
				}
				s.record("failed", "watch_error")
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			watch.Close()
			<-s.done
			claudeRefreshMu.Lock()
			s.flush()
			delete(claudeRefreshes, s)
			claudeRefreshMu.Unlock()
			s.record("stopped", "process_exit")
		})
	}, nil
}

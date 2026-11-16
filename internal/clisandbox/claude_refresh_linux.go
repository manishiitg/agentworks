//go:build linux

package clisandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"golang.org/x/sys/unix"
)

// Serialize our account updates. A canonical newer file always wins; neither
// invalid/cleared tokens nor an older runtime may replace it. No polling.
var claudeRefreshMu sync.Mutex
var claudeRefreshes = map[*claudeRefresh]bool{}

type claudeRefresh struct {
	src, dst, home, id string
	runtimeID          string
	expected           [32]byte
	tracked            bool
	watch              *fsnotify.Watcher
	done               chan struct{}
}

// Refuse symlinks in every component, not just the filename: a runtime may
// rename its .claude folder. A trusted host must never follow it elsewhere.
func credentialBytes(path string) (*os.File, []byte, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: uint64(unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK), Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, nil, fmt.Errorf("credential file unavailable: %w", err)
	}
	f := os.NewFile(uintptr(fd), "credential")
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("credential is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	after, statErr := f.Stat()
	if err != nil || statErr != nil || len(data) > 1<<20 || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		f.Close()
		return nil, nil, errors.New("credential changed while being read")
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
func claudeCredentialExpiry(data []byte) int64 {
	var doc struct {
		OAuth struct {
			Expires int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	_ = json.Unmarshal(data, &doc)
	return doc.OAuth.Expires
}

func (s *claudeRefresh) record(result, reason string) {
	event := "copy_back"
	if result == "started" || result == "stopped" {
		event = "watch"
	}
	log.Printf("[PROVIDER_CRED] event=%s provider=claude result=%s credential=%s runtime=%s reason=%s", event, result, s.id, s.runtimeID, reason)
}

// Caller holds claudeRefreshMu. Keep the runtime's regular file during a warm
// process. Re-link only at the next launch, with its fresh Landlock grants.
func (s *claudeRefresh) flush() error {
	info, err := os.Lstat(s.dst)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if !info.Mode().IsRegular() {
		s.record("failed", "runtime_not_regular")
		return errors.New("Claude refresh is not a regular file")
	}
	data, at, err := readClaudeRefreshBytes(s.dst, s.home)
	if err != nil {
		s.record("failed", "runtime_unreadable")
		return errors.New("cannot read Claude refresh")
	}
	if !validClaudeRefresh(data) {
		s.record("skipped", "invalid_refresh")
		return nil
	}
	account, current, err := credentialBytes(s.src)
	if err != nil {
		s.record("failed", "account_unavailable")
		return errors.New("canonical Claude login unavailable")
	}
	defer account.Close()
	srcInfo, err := account.Stat()
	if err != nil {
		return errors.New("canonical Claude login unavailable")
	}
	if bytes.Equal(data, current) {
		return nil
	}
	if !at.After(srcInfo.ModTime()) || claudeCredentialExpiry(data) < claudeCredentialExpiry(current) {
		s.record("skipped", "older_or_equal")
		return nil
	}
	currentHash := sha256.Sum256(current)
	if s.tracked && currentHash != s.expected {
		s.record("skipped", "account_changed")
		return nil
	}
	if !s.tracked {
		s.record("refused", "untracked_runtime")
		return errors.New("Claude has an unsaved login refresh from an older runtime. Reconnect Claude in Settings → AI accounts, then start a new chat.")
	}
	// Preserve the existing file's mode and POSIX ACL, including all slots that
	// were already admitted to this particular shared login. Never widen its parent.
	acl, err := credentialACL(account)
	if err != nil {
		s.record("failed", "account_acl")
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.src), ".agentworks-refresh-*")
	if err != nil {
		s.record("failed", "stage")
		return errors.New("cannot stage Claude refresh")
	}
	name := temp.Name()
	defer os.Remove(name)
	defer temp.Close()
	if err = temp.Chmod(srcInfo.Mode().Perm()); err == nil && len(acl) > 0 {
		err = unix.Fsetxattr(int(temp.Fd()), "system.posix_acl_access", acl, 0)
	}
	if err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if err == nil {
		err = os.Chtimes(name, at, at)
	}
	if err == nil {
		err = temp.Close()
	}
	// Do not replace a separately reconnected account observed during staging.
	now, statErr := os.Stat(s.src)
	if statErr != nil || !sameCredentialVersion(srcInfo, now) {
		s.record("skipped", "account_changed")
		return nil
	}
	if err == nil {
		err = os.Rename(name, s.src)
	}
	if err != nil {
		s.record("failed", "atomic_write")
		return errors.New("cannot save Claude refresh")
	}
	// An owner-only private-account file may have belonged to this slot. After
	// an atomic replacement the service owns it; retain the selected slot's access.
	if slot, ok := slotfs.SlotOf(s.home); ok {
		if err = slotfs.GrantAccess(slot, s.src); err != nil {
			s.record("failed", "slot_access")
			return errors.New("cannot retain selected Claude account access")
		}
	}
	next := sha256.Sum256(data)
	for active := range claudeRefreshes {
		if active.src == s.src && active.expected == currentHash {
			active.expected = next
		}
	}
	s.expected = next
	s.record("saved", "newer_refresh")
	return nil
}

// ACL/owner edits change ctime even when the login's mtime is unchanged.
func sameCredentialVersion(a, b os.FileInfo) bool {
	aa, okA := a.Sys().(*syscall.Stat_t)
	bb, okB := b.Sys().(*syscall.Stat_t)
	return okA && okB && os.SameFile(a, b) && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime()) && aa.Ctim == bb.Ctim && aa.Uid == bb.Uid && aa.Gid == bb.Gid
}

func credentialACL(f *os.File) ([]byte, error) {
	n, err := unix.Fgetxattr(int(f.Fd()), "system.posix_acl_access", nil)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil, nil
	}
	if err != nil || n > 65536 {
		return nil, errors.New("cannot read credential ACL")
	}
	out := make([]byte, n)
	n, err = unix.Fgetxattr(int(f.Fd()), "system.posix_acl_access", out)
	if err != nil {
		return nil, errors.New("cannot read credential ACL")
	}
	return out[:n], nil
}

func claudeCredentialSource(policy *llmtypes.CLISecurityPolicy) string {
	home := policy.CredentialHome
	env := os.Getenv
	if home == "" {
		home, _ = os.UserHomeDir()
	} else {
		env = func(k string) string { return policy.CredentialEnv[k] }
	}
	return canonical(credentialSource(credentialFiles["claude-code"][0], canonical(home), env))
}
func newClaudeRefresh(policy *llmtypes.CLISecurityPolicy, home string) *claudeRefresh {
	src := claudeCredentialSource(policy)
	hash := sha256.Sum256([]byte(src))
	runtimeHash := sha256.Sum256([]byte(home))
	return &claudeRefresh{runtimeID: hex.EncodeToString(runtimeHash[:8]), src: src, dst: filepath.Join(home, ".claude", ".credentials.json"), home: home, id: hex.EncodeToString(hash[:8])}
}
func flushClaudeCredentialSync(policy *llmtypes.CLISecurityPolicy) error {
	if strings.TrimSpace(policy.Provider) != "claude-code" {
		return nil
	}
	claudeRefreshMu.Lock()
	defer claudeRefreshMu.Unlock()
	source := claudeCredentialSource(policy)
	for s := range claudeRefreshes {
		if s.src == source {
			if err := s.flush(); err != nil {
				return err
			}
		}
	}
	// Also recover a replacement left by a completed/older run, before re-linking.
	s := newClaudeRefresh(policy, canonical(policy.PrivateHome))
	if canonical(s.src) == canonical(s.home) {
		return nil
	}
	return s.flush()
}
func beginClaudeCredentialSync(policy *llmtypes.CLISecurityPolicy, home string) (func(), error) {
	noop := func() {}
	if strings.TrimSpace(policy.Provider) != "claude-code" {
		return noop, nil
	}
	s := newClaudeRefresh(policy, home)
	if s.src == s.dst {
		return noop, nil
	}
	if _, err := os.Stat(s.src); os.IsNotExist(err) {
		return noop, nil
	}
	watch, err := fsnotify.NewWatcher()
	if err != nil {
		return noop, errors.New("cannot observe Claude refresh")
	}
	if err = watch.Add(filepath.Dir(s.dst)); err != nil {
		watch.Close()
		return noop, errors.New("cannot observe Claude credential folder")
	}
	s.watch = watch
	s.done = make(chan struct{})
	claudeRefreshMu.Lock()
	account, data, readErr := credentialBytes(s.src)
	if readErr != nil {
		claudeRefreshMu.Unlock()
		watch.Close()
		return noop, errors.New("cannot read canonical Claude account")
	}
	account.Close()
	s.expected = sha256.Sum256(data)
	s.tracked = true
	claudeRefreshes[s] = true
	claudeRefreshMu.Unlock()
	s.record("started", "launch")
	go func() {
		defer close(s.done)
		for {
			select {
			case event, ok := <-watch.Events:
				if !ok {
					return
				}
				if event.Name == s.dst && event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) != 0 {
					claudeRefreshMu.Lock()
					_ = s.flush()
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
			_ = s.flush()
			delete(claudeRefreshes, s)
			claudeRefreshMu.Unlock()
			s.record("stopped", "process_exit")
		})
	}, nil
}

// The replacement is created 0600 by the selected slot, so the service may
// not read it itself. Read one no-follow, single-link regular file as that slot;
// never chmod it or pass credential values to argv, env, logs or tool output.
const claudeRefreshReadScript = `import os,stat,struct,sys
fd=os.open("/",os.O_PATH|os.O_DIRECTORY)
parts=sys.argv[1].split("/")
for part in parts[1:-1]:
 if not part or part in (".",".."): sys.exit(2)
 child=os.open(part,os.O_PATH|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
 os.close(fd);fd=child
child=os.open(parts[-1],os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd);os.close(fd)
s=os.fstat(child)
if not stat.S_ISREG(s.st_mode) or s.st_nlink!=1 or s.st_size>1048576: sys.exit(2)
with os.fdopen(child,"rb") as f:
 data=f.read(1048577);end=os.fstat(f.fileno())
if len(data)>1048576 or s.st_mtime_ns!=end.st_mtime_ns or s.st_size!=end.st_size: sys.exit(2)
sys.stdout.buffer.write(struct.pack(">q",s.st_mtime_ns)+data)
`

func readClaudeRefreshBytes(path, home string) ([]byte, time.Time, error) {
	f, data, err := credentialBytes(path)
	if err == nil {
		info, e := f.Stat()
		f.Close()
		if e != nil {
			return nil, time.Time{}, e
		}
		return data, info.ModTime(), nil
	}
	if !os.IsPermission(err) || !slotfs.IsSlotLaunch(home) {
		return nil, time.Time{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-c", claudeRefreshReadScript, path)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cleanup, err := slotfs.WrapCmd(cmd, home, cmd.Env)
	if err != nil {
		return nil, time.Time{}, errors.New("cannot read selected slot refresh")
	}
	defer cleanup()
	var out cappedCredentialBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil || out.Len() < 8 {
		return nil, time.Time{}, errors.New("selected slot refresh unavailable")
	}
	stamp := int64(binary.BigEndian.Uint64(out.Bytes()[:8]))
	return out.Bytes()[8:], time.Unix(0, stamp), nil
}

type cappedCredentialBuffer struct{ bytes.Buffer }

func (b *cappedCredentialBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > (1<<20)+8 {
		return 0, errors.New("refresh too large")
	}
	return b.Buffer.Write(p)
}

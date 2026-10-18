//go:build linux

package handlers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

type relaySlotJournal struct {
	path string
	slot string
	info os.FileInfo
	uid  uint32
}

func cleanupRelaySlotRunFiles(ctx context.Context, docsRoot, target string) (bool, error) {
	if !slots.Enabled() {
		return false, nil
	}
	releases, isRelay, err := relayDeletionReleases(docsRoot, target)
	if err != nil || !isRelay {
		return false, err
	}
	journals, err := relaySlotJournals(target, releases, func(uid uint32) string {
		return relayCleanupSlot(uid, user.LookupId)
	})
	if err != nil {
		return false, err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	pending := false
	for _, journal := range journals {
		if journal.slot == "" {
			log.Printf("[RELAY_DELETE] Private journal needs administrator cleanup: uid=%d path=%s", journal.uid, journal.path)
			pending = true
			continue
		}
		runDir := filepath.Dir(journal.path)
		resolved, err := filepath.EvalSymlinks(runDir)
		if err != nil {
			return false, err
		}
		if resolved != runDir {
			return false, fmt.Errorf("Relay cleanup refuses redirected run directories")
		}
		current, err := os.Lstat(journal.path)
		if err != nil {
			return false, err
		}
		stat, ok := current.Sys().(*syscall.Stat_t)
		if !ok || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || stat.Uid != journal.uid || !os.SameFile(current, journal.info) {
			return false, fmt.Errorf("Flow private journal changed before cleanup: %s", journal.path)
		}
		iso := &security.Isolator{Slot: journal.slot, BaseDir: docsRoot, WorkDir: runDir,
			ReadPaths: []string{runDir}, WritePaths: []string{runDir}, StrictAllowlist: true}
		// The real executor holds this lock. Refuse a live run rather than
		// removing its checkpoint database underneath it. Paths are shell quoted.
		command := "flock -n -E 75 " + shellQuote(filepath.Join(journal.path, "executor.lock")) + " rm -rf -- " + shellQuote(journal.path)
		cmd, release, err := iso.ExecuteIsolated(cleanupCtx, command, nil)
		if err != nil {
			return false, err
		}
		output, runErr := cmd.CombinedOutput()
		release()
		if runErr != nil {
			return false, fmt.Errorf("remove private Relay journal as %s: %w: %s", journal.slot, runErr, strings.TrimSpace(string(output)))
		}
		log.Printf("[RELAY_DELETE] Removed private journal: slot=%s path=%s", journal.slot, journal.path)
		if _, err := os.Lstat(journal.path); !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("private Relay journal still exists after slot cleanup")
		}
	}
	return pending, nil
}

// Unassigning a slot removes its table entry, but the OS account still owns its
// files. Resolve only the journal's actual UID, never an authored account name.
// The configured deployment prefix and existing slotctl rules remain in force.
func relayCleanupSlot(uid uint32, lookup func(string) (*user.User, error)) string {
	uidText := strconv.FormatUint(uint64(uid), 10)
	account, err := lookup(uidText)
	if err != nil || account == nil || account.Uid != uidText || !slots.ValidSlot(account.Username) {
		return ""
	}
	return account.Username
}

func relaySlotJournals(target, releases string, ownerSlot func(uint32) string) ([]relaySlotJournal, error) {
	// Resolve only real directories. An authored symlink cannot redirect this
	// maintenance operation to another Relay or to an account's private files.
	checkDir := func(dir string) (bool, error) {
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("Relay cleanup requires a real directory: %s", dir)
		}
		return true, nil
	}
	if _, err := checkDir(target); err != nil {
		return nil, err
	}
	runRoots := []string{filepath.Join(target, "runs")}
	if releases != "" {
		exists, err := checkDir(releases)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("Flow release directory disappeared before cleanup: %s", releases)
		}
		versions, err := os.ReadDir(releases)
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			if !version.IsDir() {
				continue
			}
			versionRoot := filepath.Join(releases, version.Name())
			if _, err := checkDir(versionRoot); err != nil {
				return nil, err
			}
			runRoots = append(runRoots, filepath.Join(versionRoot, "runs"))
		}
	}
	var out []relaySlotJournal
	for _, runs := range runRoots {
		if exists, err := checkDir(runs); err != nil {
			return nil, err
		} else if !exists {
			continue
		}
		entries, err := os.ReadDir(runs)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			runDir := filepath.Join(runs, entry.Name())
			if _, err := checkDir(runDir); err != nil {
				return nil, err
			}
			journal := filepath.Join(runDir, ".relay_dbos")
			if exists, err := checkDir(journal); err != nil {
				return nil, err
			} else if !exists {
				continue
			}
			info, err := os.Lstat(journal)
			if err != nil {
				return nil, err
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return nil, fmt.Errorf("Relay journal ownership unavailable")
			}
			slot := ownerSlot(stat.Uid)
			if slot != "" || stat.Uid != uint32(os.Geteuid()) {
				out = append(out, relaySlotJournal{path: journal, slot: slot, uid: stat.Uid, info: info})
			}
		}
	}
	return out, nil
}

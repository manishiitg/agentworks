//go:build linux

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
}

func cleanupRelaySlotRunFiles(ctx context.Context, docsRoot, target string) error {
	if !slots.Enabled() {
		return nil
	}
	// Only a Relay root, never a parent folder containing other projects.
	rel, err := filepath.Rel(docsRoot, target)
	if err != nil || len(strings.Split(rel, string(filepath.Separator))) != 2 || !strings.HasPrefix(rel, "Workflow/") {
		return nil
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer root.Close()
	raw, err := root.ReadFile("workflow.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var manifest struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.Kind != "relay" {
		return nil
	}
	assigned, err := slots.Assigned()
	if err != nil {
		return err
	}
	owners := map[uint32]string{}
	for _, slot := range assigned {
		account, err := user.Lookup(slot)
		if err != nil {
			return err
		}
		uid, err := strconv.ParseUint(account.Uid, 10, 32)
		if err != nil {
			return err
		}
		owners[uint32(uid)] = slot
	}
	journals, err := relaySlotJournals(target, owners)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for _, journal := range journals {
		runDir := filepath.Dir(journal.path)
		resolved, err := filepath.EvalSymlinks(runDir)
		if err != nil {
			return err
		}
		if resolved != runDir {
			return fmt.Errorf("Relay cleanup refuses redirected run directories")
		}
		iso := &security.Isolator{Slot: journal.slot, BaseDir: docsRoot, WorkDir: runDir,
			ReadPaths: []string{runDir}, WritePaths: []string{runDir}, StrictAllowlist: true}
		// The real executor holds this lock. Refuse a live run rather than
		// removing its checkpoint database underneath it. Paths are shell quoted.
		command := "flock -n -E 75 " + shellQuote(filepath.Join(journal.path, "executor.lock")) + " rm -rf -- " + shellQuote(journal.path)
		cmd, release, err := iso.ExecuteIsolated(cleanupCtx, command, nil)
		if err != nil {
			return err
		}
		output, runErr := cmd.CombinedOutput()
		release()
		if runErr != nil {
			return fmt.Errorf("remove private Relay journal as %s: %w: %s", journal.slot, runErr, strings.TrimSpace(string(output)))
		}
		if _, err := os.Lstat(journal.path); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("private Relay journal still exists after slot cleanup")
		}
	}
	return nil
}

func relaySlotJournals(target string, owners map[uint32]string) ([]relaySlotJournal, error) {
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
	releases := filepath.Join(target, ".relay_releases")
	if exists, err := checkDir(releases); err != nil {
		return nil, err
	} else if exists {
		versions, err := os.ReadDir(releases)
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			if !version.IsDir() {
				continue
			}
			runRoots = append(runRoots, filepath.Join(releases, version.Name(), "runs"))
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
			journal := filepath.Join(runs, entry.Name(), ".relay_dbos")
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
			if slot := owners[stat.Uid]; slot != "" {
				out = append(out, relaySlotJournal{path: journal, slot: slot})
			}
		}
	}
	return out, nil
}

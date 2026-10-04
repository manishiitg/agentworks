package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/spf13/cobra"
)

var migrateDurableChatsCmd = &cobra.Command{
	Use:   "migrate-chat-events",
	Short: "Import legacy interactive chat JSON into the canonical SQLite log",
	RunE: func(cmd *cobra.Command, _ []string) error {
		docsRoot, _ := cmd.Flags().GetString("docs-root")
		stateRoot, _ := cmd.Flags().GetString("state-root")
		if strings.TrimSpace(docsRoot) == "" {
			docsRoot = fsutil.WorkspaceDocsRoot()
		}
		if strings.TrimSpace(stateRoot) == "" {
			var err error
			stateRoot, err = workflowCLIStateRoot()
			if err != nil {
				return err
			}
		}
		force, _ := cmd.Flags().GetBool("force")
		return migrateDurableChatsFromDisk(filepath.Clean(docsRoot), filepath.Clean(stateRoot), force)
	},
}

func init() {
	migrateDurableChatsCmd.Flags().String("docs-root", "", "absolute workspace documents root")
	migrateDurableChatsCmd.Flags().String("state-root", "", "absolute AgentWorks state root")
	migrateDurableChatsCmd.Flags().Bool("force", false, "rerun even when the one-time marker exists")
}

// errDurableChatDocsRootMissing leaves the one-time marker unwritten: a docs
// root that is not mounted yet is "not yet scanned", not "nothing to import".
var errDurableChatDocsRootMissing = errors.New("workspace docs root does not exist")

// migrateDurableChatsAtStartup covers every launch path (deploy scripts,
// Dominion, desktop, dedicated VM) before the durable journal is opened and
// before the listener accepts requests. It is a no-op once the marker exists.
// Failures are logged, never fatal: the per-session migration state lets the
// next start resume, and refusing to serve would turn an import problem into
// an outage.
func migrateDurableChatsAtStartup(stateRoot string) {
	docsRoot, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		log.Printf("[CHAT_MIGRATION] cannot resolve workspace docs root: %v", err)
		return
	}
	if err := migrateDurableChatsFromDisk(docsRoot, filepath.Clean(stateRoot), false); err != nil {
		if errors.Is(err, errDurableChatDocsRootMissing) {
			log.Printf("[CHAT_MIGRATION] skipped: %v (%s); will retry on next start", err, docsRoot)
			return
		}
		log.Printf("[CHAT_MIGRATION] legacy chat import failed; will retry on next start: %v", err)
	}
}

type durableChatMigrationCandidate struct {
	path    string
	updated time.Time
	ownerID string
}

type durableChatMigrationStats struct {
	scanned, migrated, skipped, failed int
}

func migrateDurableChatsFromDisk(docsRoot, stateRoot string, force bool) error {
	if !filepath.IsAbs(docsRoot) || !filepath.IsAbs(stateRoot) {
		return fmt.Errorf("docs-root and state-root must be absolute")
	}
	markerPath := filepath.Join(stateRoot, "migrations", "chat-events-v2.done")
	if !force {
		if _, err := os.Stat(markerPath); err == nil {
			fmt.Printf("durable chat migration already complete: marker=%s\n", markerPath)
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if info, err := os.Stat(docsRoot); err != nil || !info.IsDir() {
		return fmt.Errorf("%w: %s", errDurableChatDocsRootMissing, docsRoot)
	}
	var stats durableChatMigrationStats
	candidates := collectDurableChatMigrationCandidates(docsRoot, &stats)

	journal, err := internalevents.OpenSQLiteEventJournal(filepath.Join(stateRoot, "structured-chat-events-v2.sqlite"))
	if err != nil {
		return err
	}
	store := internalevents.NewEventStore(1)
	store.SetDurableJournal(journal)
	defer store.Stop()

	sessionIDs := make([]string, 0, len(candidates))
	for sessionID := range candidates {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	stats.scanned = len(sessionIDs)
	for _, sessionID := range sessionIDs {
		candidate := candidates[sessionID]
		complete, stateErr := store.DurableChatMigrationComplete(sessionID)
		if stateErr != nil {
			return stateErr
		}
		if complete && !force {
			stats.skipped++
			continue
		}
		// Read one file at a time: holding every candidate's bytes during the
		// walk made memory grow with the whole legacy archive.
		raw, readErr := readConversationFileDirect(candidate.path)
		if readErr != nil {
			log.Printf("[CHAT_MIGRATION] skipping unreadable %s: %v", candidate.path, readErr)
			stats.failed++
			continue
		}
		events, decodeErr := decodeLegacyChatEvents(raw, sessionID)
		if decodeErr != nil {
			log.Printf("[CHAT_MIGRATION] skipping malformed %s: %v", candidate.path, decodeErr)
			stats.failed++
			continue
		}
		if err := store.SetSessionPersistenceClass(sessionID, internalevents.SessionPersistenceInteractiveChat); err != nil {
			return err
		}
		store.SetSessionOwner(sessionID, candidate.ownerID)
		// File-level problems are skipped above; a journal error here is
		// systemic (disk, database), so stop without writing the marker.
		if importErr := store.ImportDurableChatEvents(sessionID, events); importErr != nil {
			return fmt.Errorf("migrate %s: %w", candidate.path, importErr)
		}
		stats.migrated++
	}
	if err := journal.Checkpoint(); err != nil {
		return fmt.Errorf("checkpoint durable chat journal: %w", err)
	}
	fmt.Printf("durable chat migration complete: scanned=%d migrated=%d skipped=%d failed=%d database=%s\n", stats.scanned, stats.migrated, stats.skipped, stats.failed, filepath.Join(stateRoot, "structured-chat-events-v2.sqlite"))
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(markerPath), ".chat-events-v2.*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := fmt.Fprintf(temporary, "completed_at=%s\nscanned=%d\nmigrated=%d\nskipped=%d\nfailed=%d\n", time.Now().UTC().Format(time.RFC3339), stats.scanned, stats.migrated, stats.skipped, stats.failed); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, markerPath)
}

// collectDurableChatMigrationCandidates picks the newest copy of each
// interactive session by header timestamp, keeping only paths. One bad file or
// unreadable directory is logged and counted, never fatal to the whole scan.
func collectDurableChatMigrationCandidates(docsRoot string, stats *durableChatMigrationStats) map[string]durableChatMigrationCandidate {
	candidates := make(map[string]durableChatMigrationCandidate)
	_ = filepath.WalkDir(docsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			log.Printf("[CHAT_MIGRATION] skipping unreadable %s: %v", path, walkErr)
			stats.failed++
			if entry != nil && entry.IsDir() && path != docsRoot {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") || !strings.HasSuffix(entry.Name(), "-conversation.json") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			log.Printf("[CHAT_MIGRATION] skipping unreadable %s: %v", path, readErr)
			stats.failed++
			return nil
		}
		var header struct {
			SessionID string `json:"session_id"`
			UpdatedAt string `json:"updated_at"`
			UserID    string `json:"user_id"`
		}
		if jsonErr := json.Unmarshal(raw, &header); jsonErr != nil {
			log.Printf("[CHAT_MIGRATION] skipping malformed %s: %v", path, jsonErr)
			stats.failed++
			return nil
		}
		sessionID := strings.TrimSpace(header.SessionID)
		if sessionID == "" || strings.HasPrefix(sessionID, "schedule-") || strings.HasPrefix(sessionID, "sched_") || strings.HasPrefix(sessionID, "bot-") {
			stats.skipped++
			return nil
		}
		updated, _ := time.Parse(time.RFC3339Nano, header.UpdatedAt)
		if updated.IsZero() {
			if info, infoErr := entry.Info(); infoErr == nil {
				updated = info.ModTime()
			}
		}
		if previous, exists := candidates[sessionID]; !exists || updated.After(previous.updated) {
			ownerID := strings.TrimSpace(header.UserID)
			if ownerID == "" {
				ownerID = legacyChatOwnerFromPath(path)
			}
			candidates[sessionID] = durableChatMigrationCandidate{path: path, updated: updated, ownerID: ownerID}
		}
		return nil
	})
	return candidates
}

func legacyChatOwnerFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for index, part := range parts {
		if (part == workspaceref.UsersDir || part == "users") && index+1 < len(parts) && strings.TrimSpace(parts[index+1]) != "" {
			return parts[index+1]
		}
	}
	return "default"
}

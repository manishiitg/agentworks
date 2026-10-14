package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Freshness ledger — a code-owned record of when a workflow's learnings and
// knowledgebase stores (and each item within them) were last confirmed by an
// actual run.
//
// Unlike the LLM-maintained notes/_index.json provenance (which the agent
// recomputes with wc/grep/date and can silently desync), this ledger is written
// by Go at contribution-turn completion, so it cannot drift. It records
// CONFIRMATION recency, not calendar age: a run that reviews a store and leaves
// it current confirms the store; a run that rewrites a specific reference file
// or note confirms that item. Knowledge exercised every run stays fresh;
// knowledge no run re-touches ages. Pulse learning_health / knowledgebase_health
// read this to age out (re-verify / demote) knowledge no run has re-confirmed.
//
// Per-item confirmation is derived from observable content-hash changes, not the
// LLM's self-report: an item whose file content changed this run was updated
// (definitely current); an item never rewritten again keeps its old stamp and
// visibly ages, which is exactly the orphaned-knowledge signal we want.

const freshnessLedgerFileName = "_freshness.json"

// Confirmation action verbs.
const (
	freshnessActionUpdated            = "updated"             // the turn/item changed
	freshnessActionConfirmedUnchanged = "confirmed_unchanged" // reviewed, nothing to change
	freshnessActionReviewed           = "reviewed"            // reviewed; update-vs-unchanged not distinguished
)

// freshnessHistoryCap bounds the retained store-level confirmation history so the
// ledger stays a small file.
const freshnessHistoryCap = 25

// freshnessLedgerMu serializes the read-modify-write of the small ledger files
// across parallel steps within one run.
var freshnessLedgerMu sync.Mutex

// FreshnessEntry is one recorded store-level confirmation by a run/step.
type FreshnessEntry struct {
	Run    string `json:"run"`
	At     string `json:"at"`
	Step   string `json:"step,omitempty"`
	Action string `json:"action"`
}

// ItemFreshness tracks confirmation recency for a single knowledge item
// (a learnings reference file or a KB topic note), keyed by its relative path.
type ItemFreshness struct {
	Hash             string `json:"hash"`
	LastConfirmedRun string `json:"last_confirmed_run,omitempty"`
	LastConfirmedAt  string `json:"last_confirmed_at,omitempty"`
	ConfirmCount     int    `json:"confirm_count"`
	LastAction       string `json:"last_action,omitempty"`
}

// FreshnessLedger is the code-owned freshness record for one store.
type FreshnessLedger struct {
	Store            string                   `json:"store"` // "learnings" | "knowledgebase"
	LastConfirmedRun string                   `json:"last_confirmed_run,omitempty"`
	LastConfirmedAt  string                   `json:"last_confirmed_at,omitempty"`
	ConfirmCount     int                      `json:"confirm_count"`
	UpdatedCount     int                      `json:"updated_count"`
	Items            map[string]ItemFreshness `json:"items,omitempty"`
	History          []FreshnessEntry         `json:"history,omitempty"`
}

func learningsFreshnessLedgerPath() string {
	return filepath.Join("learnings", GlobalLearningID, freshnessLedgerFileName)
}

func knowledgebaseFreshnessLedgerPath() string {
	return filepath.Join(KnowledgebaseFolderName, freshnessLedgerFileName)
}

// parseFreshnessLedger tolerantly parses existing ledger JSON. Empty or malformed
// content yields a fresh ledger rather than dropping the confirmation.
func parseFreshnessLedger(existingJSON, store string) FreshnessLedger {
	ledger := FreshnessLedger{Store: store}
	if strings.TrimSpace(existingJSON) != "" {
		var existing FreshnessLedger
		if json.Unmarshal([]byte(existingJSON), &existing) == nil {
			ledger = existing
			ledger.Store = store // keep the canonical store label
		}
	}
	return ledger
}

func marshalFreshnessLedger(ledger FreshnessLedger) (string, error) {
	payload, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal freshness ledger: %w", err)
	}
	return string(payload) + "\n", nil
}

// stampStoreConfirmation records one store-level confirmation on the ledger.
func stampStoreConfirmation(ledger *FreshnessLedger, runFolder, stepID, action, nowRFC3339 string) {
	runFolder = strings.TrimSpace(runFolder)
	stepID = strings.TrimSpace(stepID)
	action = strings.TrimSpace(action)
	if action == "" {
		action = freshnessActionReviewed
	}
	ledger.LastConfirmedAt = nowRFC3339
	if runFolder != "" {
		ledger.LastConfirmedRun = runFolder
	}
	ledger.ConfirmCount++
	if action == freshnessActionUpdated {
		ledger.UpdatedCount++
	}
	ledger.History = append(ledger.History, FreshnessEntry{
		Run:    runFolder,
		At:     nowRFC3339,
		Step:   stepID,
		Action: action,
	})
	if len(ledger.History) > freshnessHistoryCap {
		ledger.History = ledger.History[len(ledger.History)-freshnessHistoryCap:]
	}
}

// applyFreshnessConfirmation is the pure store-level transform, retained for the
// simple record path and unit tests.
func applyFreshnessConfirmation(existingJSON, store, runFolder, stepID, action, nowRFC3339 string) (string, error) {
	ledger := parseFreshnessLedger(existingJSON, store)
	stampStoreConfirmation(&ledger, runFolder, stepID, action, nowRFC3339)
	return marshalFreshnessLedger(ledger)
}

// applyItemFreshness reconciles the per-item records against the current on-disk
// item hashes: a new or content-changed item is stamped `updated` for this run;
// an unchanged item keeps its prior stamp; an item no longer on disk is dropped
// (retired). It never advances the stamp of an untouched item — that is the
// signal a reviewer uses to find orphaned, no-longer-maintained knowledge.
func applyItemFreshness(existing map[string]ItemFreshness, current map[string]string, runFolder, stepID, nowRFC3339 string) map[string]ItemFreshness {
	runFolder = strings.TrimSpace(runFolder)
	if len(current) == 0 {
		// Nothing to reconcile against (dir missing / unreadable): preserve prior
		// item records rather than wiping them.
		return existing
	}
	out := make(map[string]ItemFreshness, len(current))
	for path, hash := range current {
		if prev, ok := existing[path]; ok && prev.Hash == hash {
			out[path] = prev // unchanged this run — keep the older stamp so it can age
			continue
		}
		prevCount := 0
		if prev, ok := existing[path]; ok {
			prevCount = prev.ConfirmCount
		}
		out[path] = ItemFreshness{
			Hash:             hash,
			LastConfirmedRun: runFolder,
			LastConfirmedAt:  nowRFC3339,
			ConfirmCount:     prevCount + 1,
			LastAction:       freshnessActionUpdated,
		}
	}
	return out
}

// hashContent returns a short content hash used to detect item changes.
func hashContent(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// snapshotItemHashes returns relPath->contentHash for every `.md` knowledge file
// under absDir (recursively). Infra files (_index.json, _freshness.json,
// .learning_metadata.json, dotfiles) are non-`.md` and thus naturally excluded.
// A missing directory is not an error (returns an empty map).
func snapshotItemHashes(absDir string) (map[string]string, error) {
	out := map[string]string{}
	walkErr := filepath.WalkDir(absDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate unreadable entries; best-effort snapshot
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
			return nil
		}
		content, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(absDir, p)
		if relErr != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = hashContent(content)
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return out, walkErr
	}
	return out, nil
}

// recordStoreConfirmation reads the ledger, records one store-level confirmation
// and reconciles per-item freshness from the current on-disk item hashes, then
// writes it back. Best-effort: a read/parse miss starts a fresh ledger; a
// snapshot error skips the per-item pass but still records the store-level
// confirmation. Callers log (never fail the run on) a write error.
func (hcpo *StepBasedWorkflowOrchestrator) recordStoreConfirmation(
	ctx context.Context,
	store string,
	ledgerPath string,
	itemsAbsDir string,
	runFolder string,
	stepID string,
	action string,
) error {
	freshnessLedgerMu.Lock()
	defer freshnessLedgerMu.Unlock()

	existing := ""
	if content, err := hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, ledgerPath); err == nil {
		existing = content
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ledger := parseFreshnessLedger(existing, store)
	stampStoreConfirmation(&ledger, runFolder, stepID, action, now)

	if strings.TrimSpace(itemsAbsDir) != "" {
		if current, snapErr := snapshotItemHashes(itemsAbsDir); snapErr == nil {
			ledger.Items = applyItemFreshness(ledger.Items, current, runFolder, stepID, now)
		}
	}

	updated, err := marshalFreshnessLedger(ledger)
	if err != nil {
		return err
	}
	if err := hcpo.BaseOrchestrator.WriteWorkspaceFile(ctx, ledgerPath, updated); err != nil {
		return fmt.Errorf("write freshness ledger %s: %w", ledgerPath, err)
	}
	return nil
}

// learningsItemsAbsDir / knowledgebaseItemsAbsDir resolve the absolute on-disk
// directories whose `.md` files are the per-item knowledge units.
func (hcpo *StepBasedWorkflowOrchestrator) learningsItemsAbsDir() string {
	return filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), "learnings", GlobalLearningID)
}

func (hcpo *StepBasedWorkflowOrchestrator) knowledgebaseItemsAbsDir() string {
	return filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), KnowledgebaseFolderName, KBNotesFolderName)
}

// recordLearningsConfirmation stamps that a run re-confirmed the learnings store,
// distinguishing an actual update from a reviewed-unchanged confirmation, and
// reconciles per-reference-file freshness.
func (hcpo *StepBasedWorkflowOrchestrator) recordLearningsConfirmation(ctx context.Context, runFolder, stepID string, updated bool) error {
	action := freshnessActionConfirmedUnchanged
	if updated {
		action = freshnessActionUpdated
	}
	return hcpo.recordStoreConfirmation(ctx, "learnings", learningsFreshnessLedgerPath(), hcpo.learningsItemsAbsDir(), runFolder, stepID, action)
}

// recordKnowledgebaseConfirmation stamps that a run reviewed the knowledgebase
// store and reconciles per-topic-note freshness. v1 does not distinguish updated
// vs unchanged at the store level for KB; per-item update detection is by hash.
func (hcpo *StepBasedWorkflowOrchestrator) recordKnowledgebaseConfirmation(ctx context.Context, runFolder, stepID string) error {
	return hcpo.recordStoreConfirmation(ctx, "knowledgebase", knowledgebaseFreshnessLedgerPath(), hcpo.knowledgebaseItemsAbsDir(), runFolder, stepID, freshnessActionReviewed)
}

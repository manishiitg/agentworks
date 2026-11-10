package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	"github.com/spf13/cobra"
)

type registryCheckReport struct {
	User        string         `json:"user"`
	Current     int            `json:"current"`
	Previous    int            `json:"previous"`
	Identities  int            `json:"identities"`
	Differences map[string]int `json:"differences"`
	Ambiguous   int            `json:"ambiguous"`
}

func init() { ServerCmd.AddCommand(newRegistryCheckCommand()) }
func newRegistryCheckCommand() *cobra.Command {
	var docs string
	cmd := &cobra.Command{Use: "registry-check", Short: "Compare saved chat bindings without changing the server", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if docs == "" {
			docs = fsutil.WorkspaceDocsRoot()
		}
		// This command never starts the server. Keep legacy reader debug logs
		// from printing a malformed upstream response containing registry content.
		previousLog := log.Writer()
		log.SetOutput(io.Discard)
		defer log.SetOutput(previousLog)
		return checkConversationRegistries(cmd.Context(), docs, readFileFromWorkspace, cmd.OutOrStdout())
	}}
	cmd.Flags().StringVar(&docs, "docs-root", "", "Workspace documents directory (uses the server environment by default)")
	return cmd
}

// This explicit operator command is read-only on live state. It reads one
// existing JSON snapshot per user through the old reader and seeds only an
// in-memory candidate. It never calls activateConversationRegistryRows.
func checkConversationRegistries(ctx context.Context, docs string, read func(context.Context, string) (string, bool, error), out io.Writer) error {
	docs, err := sqliteopen.CanonicalPath(docs)
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(docs, "_users", "*", "chat_history", productConversationRegistryFile))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no saved chat registries found; check the workspace documents directory")
	}
	failed := 0
	for _, file := range files {
		user := filepath.Base(filepath.Dir(filepath.Dir(file)))
		logical, err := filepath.Rel(docs, file)
		if err != nil {
			return err
		}
		logical = filepath.ToSlash(logical)
		report := registryCheckReport{User: user, Differences: map[string]int{}}
		// Resolve the admitted namespace before requesting the old HTTP path, so
		// directory links cannot select another user's registry during discovery.
		local, e := fsutil.WorkspacePath(docs, logical)
		if e != nil {
			report.Differences["registry_path"] = 1
		} else if info, e := os.Lstat(local); e != nil || !info.Mode().IsRegular() {
			report.Differences["registry_path"] = 1
		} else {
			raw, exists, e := read(ctx, logical)
			if e != nil || !exists {
				report.Differences["registry_read"] = 1
			} else {
				document, e := decodeConversationRegistrySnapshot(raw)
				if e != nil {
					report.Differences["registry_decode"] = 1
				} else {
					// Prove the old HTTP path and the future filesystem importer selected
					// the same installation/snapshot. A concurrent change is a failed check,
					// never permission to import a different set of identity bindings.
					localRaw, e := os.ReadFile(local)
					sameSnapshot, decodeErr := sameRegistryJSON(raw, string(localRaw))
					if e != nil || decodeErr != nil || !sameSnapshot {
						report.Differences["source_snapshot_changed"] = 1
					} else {
						report.Current = len(document.Entries)
						report.Identities = len(document.Agents)
						for _, prior := range document.Previous {
							report.Previous += len(prior)
						}
						report.Differences, report.Ambiguous, e = compareConversationRegistrySnapshot(ctx, user, logical, raw, document, read)
						if e != nil {
							report.Differences = map[string]int{"candidate_database": 1}
						}
					}
				}
			}
		}

		if len(report.Differences) > 0 || report.Ambiguous > 0 {
			failed++
		}
		if err = json.NewEncoder(out).Encode(report); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("chat registry comparison needs attention for %d user(s)", failed)
	}
	return nil
}

type registrySnapshotRead struct {
	raw    string
	exists bool
	err    error
}
type registryHistoryComparison struct {
	Current  ProductConversationRecord
	Exists   bool
	Previous []ProductConversationRecord
}

func compareConversationRegistrySnapshot(ctx context.Context, user, namespace, raw string, document productConversationRegistryDocument, read func(context.Context, string) (string, bool, error)) (map[string]int, int, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	candidate := conversationRegistryRows{db: db, namespace: namespace}
	if err = candidate.importSnapshot(ctx, raw); err != nil {
		return nil, 0, err
	}
	// A diagnostic snapshot is not a live document cache. Both getters see the
	// exact same source JSON and each requested product manifest for this check.
	snapshots := map[string]registrySnapshotRead{namespace: {raw: raw, exists: true}}
	snapshotRead := func(ctx context.Context, path string) (string, bool, error) {
		if saved, ok := snapshots[path]; ok {
			return saved.raw, saved.exists, saved.err
		}
		value, exists, e := read(ctx, path)
		snapshots[path] = registrySnapshotRead{value, exists, e}
		return value, exists, e
	}
	old := productConversationRegistryStore{read: snapshotRead}
	differences := map[string]int{}
	compare := func(category string, a, b any, oldErr, newErr error) {
		if oldErr != nil || newErr != nil {
			differences[category+"_read_error"]++
			return
		}
		if !reflect.DeepEqual(a, b) {
			differences[category]++
		}
	}
	keys := map[string]bool{}
	for key := range document.Entries {
		keys[key] = true
	}
	for key := range document.Previous {
		keys[key] = true
	}
	for key := range document.Agents {
		keys[key] = true
	}
	profiles := map[string]bool{}
	projects := map[string]map[string]bool{}
	for entry := range keys {
		profile, key, ok := strings.Cut(entry, "/")
		if !ok || profile == "" || key == "" {
			differences["unclassified_key"]++
			continue
		}
		profiles[profile] = true
		a, e := old.agentIdentity(ctx, user, profile, key)
		b, f := candidate.agentIdentity(ctx, profile, key)
		compare("identity", a, b, e, f)
		current, exists, previous, e := old.history(ctx, user, agentprofiles.Profile{ID: profile}, productConversationBinding{ConversationKey: key})
		next, yes, prior, f := candidate.history(ctx, profile, key)
		compare("history", registryHistoryComparison{current, exists, previous}, registryHistoryComparison{next, yes, prior}, e, f)
		project, _, _ := strings.Cut(key, ":")
		if project != "" {
			if projects[profile] == nil {
				projects[profile] = map[string]bool{}
			}
			projects[profile][project] = true
		}
	}
	for profile := range profiles {
		a, e := old.liveSessionIDs(ctx, user, profile)
		b, f := candidate.liveSessionIDs(ctx, profile)
		compare("profile_sessions", a, b, e, f)
		for project := range projects[profile] {
			a, e := old.agentIdentities(ctx, user, profile, project)
			b, f := candidate.agentIdentities(ctx, profile, project)
			compare("project_identities", a, b, e, f)
			before, e := old.projectChats(ctx, user, profile, project)
			after, f := candidate.projectChats(ctx, profile, project, snapshotRead)
			compare("project_chats", registryComparableChats(before), registryComparableChats(after), e, f)
			first, e := old.scheduleOwnChats(ctx, user, profile, project)
			second, f := candidate.scheduleOwnChats(ctx, profile, project)
			compare("schedule_chats", first, second, e, f)
		}
	}
	// The old getter's map traversal is unspecified when multiple current slots
	// share these fields. Report the ambiguous groups and refuse activation;
	// neither the diagnostic nor the candidate reader guesses a winner.
	type runKey struct{ session, workspace string }
	matches := map[runKey][]ProductConversationRecord{}
	for _, record := range document.Entries {
		if record.ProfileID == crewProfileID && strings.TrimSpace(record.SessionID) != "" {
			key := runKey{record.SessionID, record.WorkspacePath}
			matches[key] = append(matches[key], record)
		}
	}
	ambiguous := 0
	for key, records := range matches {
		if len(records) > 1 {
			ambiguous++
			continue
		}
		workflow := ""
		if records[0].CurrentWorkflowRun != nil {
			workflow = records[0].CurrentWorkflowRun.WorkflowID
		}
		a, e := old.workflowStepRun(ctx, user, key.session, key.workspace, workflow, nil)
		b, f := candidate.workflowStepRun(ctx, key.session, key.workspace, workflow)
		compare("selected_workflow_run", a, b, e, f)
	}
	return differences, ambiguous, nil
}

func registryComparableChats(chats []ProductConversationRecord) []string {
	var out []string
	for _, chat := range chats {
		encoded, _ := json.Marshal(struct {
			Main   bool
			Record ProductConversationRecord
		}{chat.IsMainChat, chat})
		out = append(out, string(encoded))
	}
	sort.Strings(out)
	return out
}

// Include unknown fields and explicit empty/null values when checking that
// HTTP and filesystem selected the same source, without printing either one.
func sameRegistryJSON(first, second string) (bool, error) {
	decode := func(raw string) (any, error) {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, errors.New("multiple registry values")
			}
			return nil, err
		}
		return value, nil
	}
	a, err := decode(first)
	if err != nil {
		return false, err
	}
	b, err := decode(second)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(a, b), nil
}

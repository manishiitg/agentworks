package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// PLAT-746-827: one generated snapshot matrix compares the actual old getters
// with candidate SQL reads. It is deliberately not a live import or switch.
// The current store remains authoritative until a separately reviewed server
// registry-check proves equivalence before migration.
func TestConversationRegistryRowsMatchExistingGetterRules(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	old, files := memoryProductConversationStore()
	const project = "project_a"
	const mainKey = project + ":chat:main"
	const crew = "Crew/example"
	record := func(user, key string) ProductConversationRecord {
		return ProductConversationRecord{
			ConversationID: user + "-conversation-" + key, ConversationKey: key, ProfileID: crewProfileID, ProfileVersion: 7,
			SessionID: user + "-session-" + key, WorkspacePath: crew, ResourceID: project, Title: user + " " + key, Description: "saved role",
			ConnectionID: user + "-account", Provider: "example-cli", ModelID: "example-model", ReasoningEffort: "medium", FreshNative: true,
			EnabledServers: []string{"service-a"}, SelectedSkills: []string{"skill-a"}, SkillFilesDigest: "digest", WorkflowContextPaths: []string{"Workflow/example"},
			CurrentWorkflowRun: &productConversationWorkflowRun{WorkflowID: "workflow", Folder: "run-example"}, CreatedAt: "2026-11-10T01:00:00Z", UpdatedAt: "2026-11-10T02:00:00Z",
		}
	}
	for _, user := range []string{"owner", "run", "other", "empty", "null"} {
		doc := productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{}, Previous: map[string][]ProductConversationRecord{}, Agents: map[string]ProductAgentIdentity{}}
		if user != "empty" && user != "null" {
			keys := []string{mainKey, project + ":chat:side", project + ":chat:child:nested", project + ":schedule:kept", project + ":schedule:kept:one-run", project + ":function:f:call", project + ":webhook:w:call", project + "_more:chat:other"}
			if user == "run" {
				keys = append(keys, project)
			} // old Run person's own root retains Main
			for i, key := range keys {
				entry := productConversationRegistryEntryKey(crewProfileID, key)
				doc.Entries[entry] = record(user, key)
				doc.Agents[entry] = ProductAgentIdentity{Name: user + " " + key, Purpose: "saved purpose", UpdatedBy: user, UpdatedAt: "2026-11-10T00:00:00Z", Position: i, Unpinned: i%2 == 0, OnlyMe: true, ConnectionID: user + "-identity-account", Engine: "example-cli", ModelID: "example-model", ReasoningEffort: "high", ClaudeTerminal: true}
			}
			blank := record(user, project+":chat:blank")
			blank.SessionID = "  "
			doc.Entries[crewProfileID+"/"+blank.ConversationKey] = blank
			// Key authority differs by getter: project/profile lists use the key;
			// workflowStepRun deliberately finds by the record's profile/session/root.
			mismatched := record(user, "mismatched-record-key")
			mismatched.ProfileID = "another-profile"
			doc.Entries[crewProfileID+"/"+project+":chat:mismatch"] = mismatched
			offKey := record(user, "off-key-record")
			offKey.SessionID = user + "-off-key-session"
			doc.Entries["another-profile/"+project+":chat:off-key"] = offKey
			doc.Entries[crewProfileID+"_other/"+project+":chat:wrong-profile-prefix"] = record(user, "wrong-profile-prefix")
			doc.Agents[crewProfileID+"/"+project+":chat:orphan"] = ProductAgentIdentity{Name: user + " orphan"}
			for i := 0; i < 205; i++ {
				prior := record(user, fmt.Sprintf("previous-%03d", i))
				if i == 3 || i == 4 {
					prior.SessionID = "duplicate-previous-session"
				}
				doc.Previous[crewProfileID+"/"+mainKey] = append(doc.Previous[crewProfileID+"/"+mainKey], prior)
			}
		} else if user == "null" {
			doc.Entries = nil
			doc.Previous = nil
			doc.Agents = nil
		}
		raw, e := json.Marshal(doc)
		if e != nil {
			t.Fatal(e)
		}
		// A real source may explicitly encode an empty optional selection. Do
		// not let import turn [] into a missing field through omitempty tags.
		if user == "owner" {
			raw = []byte(strings.ReplaceAll(string(raw), `"selected_skills":["skill-a"]`, `"selected_skills":[]`))
		}
		namespace := productConversationRegistryPath(user)
		files[namespace] = string(raw)
		if err = (conversationRegistryRows{db: db, namespace: namespace}).importSnapshot(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	manifest, _ := json.Marshal(productProjectManifest{MainChatKey: mainKey})
	files[crew+"/product.json"] = string(manifest)
	rowsFor := func(user string) conversationRegistryRows {
		return conversationRegistryRows{db: db, namespace: productConversationRegistryPath(user)}
	}
	type history struct {
		Current  ProductConversationRecord
		Exists   bool
		Previous []ProductConversationRecord
	}
	normalizedChats := func(records []ProductConversationRecord) []ProductConversationRecord {
		sort.Slice(records, func(i, j int) bool { return records[i].ConversationID < records[j].ConversationID })
		return records
	}
	type equivalence struct {
		name    string
		compare func(string) (any, any, error, error)
	}
	cases := []equivalence{
		{"one identity", func(user string) (any, any, error, error) {
			a, e := old.agentIdentity(ctx, user, crewProfileID, mainKey)
			b, f := rowsFor(user).agentIdentity(ctx, crewProfileID, mainKey)
			return a, b, e, f
		}},
		{"missing identity", func(user string) (any, any, error, error) {
			a, e := old.agentIdentity(ctx, user, crewProfileID, "absent")
			b, f := rowsFor(user).agentIdentity(ctx, crewProfileID, "absent")
			return a, b, e, f
		}},
		{"project identities include saved orphans", func(user string) (any, any, error, error) {
			a, e := old.agentIdentities(ctx, user, crewProfileID, project)
			b, f := rowsFor(user).agentIdentities(ctx, crewProfileID, project)
			return a, b, e, f
		}},
		{"ordered prior chats without import truncation or deduplication", func(user string) (any, any, error, error) {
			a, ok, previous, e := old.history(ctx, user, agentprofiles.Profile{ID: crewProfileID}, productConversationBinding{ConversationKey: mainKey})
			b, yes, prior, f := rowsFor(user).history(ctx, crewProfileID, mainKey)
			return history{a, ok, previous}, history{b, yes, prior}, e, f
		}},
		{"missing slot history", func(user string) (any, any, error, error) {
			a, ok, previous, e := old.history(ctx, user, agentprofiles.Profile{ID: crewProfileID}, productConversationBinding{ConversationKey: "absent"})
			b, yes, prior, f := rowsFor(user).history(ctx, crewProfileID, "absent")
			return history{a, ok, previous}, history{b, yes, prior}, e, f
		}},
		{"profile sessions use entry key and exclude blank sessions", func(user string) (any, any, error, error) {
			a, e := old.liveSessionIDs(ctx, user, crewProfileID)
			b, f := rowsFor(user).liveSessionIDs(ctx, crewProfileID)
			return a, b, e, f
		}},
		{"main role and side chats exclude isolated keys", func(user string) (any, any, error, error) {
			a, e := old.projectChats(ctx, user, crewProfileID, project)
			b, f := rowsFor(user).projectChats(ctx, crewProfileID, project, old.read)
			return normalizedChats(a), normalizedChats(b), e, f
		}},
		{"similarly prefixed project stays separate", func(user string) (any, any, error, error) {
			a, e := old.projectChats(ctx, user, crewProfileID, project+"_more")
			b, f := rowsFor(user).projectChats(ctx, crewProfileID, project+"_more", old.read)
			return normalizedChats(a), normalizedChats(b), e, f
		}},
		{"kept schedule chat excludes per-run function webhook chats", func(user string) (any, any, error, error) {
			a, e := old.scheduleOwnChats(ctx, user, crewProfileID, project)
			b, f := rowsFor(user).scheduleOwnChats(ctx, crewProfileID, project)
			return a, b, e, f
		}},
		{"manual run lookup uses record fields even with a different stored key", func(user string) (any, any, error, error) {
			a, e := old.workflowStepRun(ctx, user, user+"-off-key-session", crew, "workflow", nil)
			b, f := rowsFor(user).workflowStepRun(ctx, user+"-off-key-session", crew, "workflow")
			return a, b, e, f
		}},
		{"manual run of a different workflow has no selected folder", func(user string) (any, any, error, error) {
			a, e := old.workflowStepRun(ctx, user, user+"-off-key-session", crew, "other-workflow", nil)
			b, f := rowsFor(user).workflowStepRun(ctx, user+"-off-key-session", crew, "other-workflow")
			return a, b, e, f
		}},
	}
	for _, user := range []string{"owner", "run", "other", "empty", "null", "missing"} {
		for _, tc := range cases {
			t.Run(user+"/"+tc.name, func(t *testing.T) {
				a, b, oldErr, newErr := tc.compare(user)
				if (oldErr != nil) != (newErr != nil) {
					t.Fatalf("refusal changed: old=%v new=%v", oldErr, newErr)
				}
				if oldErr == nil && !reflect.DeepEqual(a, b) {
					t.Fatalf("getter result changed: old=%+v new=%+v", a, b)
				}
			})
		}
	}
	t.Run("malformed snapshot fails closed without replacing imported bindings", func(t *testing.T) {
		rows := rowsFor("owner")
		before, err := rows.agentIdentity(ctx, crewProfileID, mainKey)
		if err != nil {
			t.Fatal(err)
		}
		if err = rows.importSnapshot(ctx, `{"entries":{"broken":`); err == nil {
			t.Fatal("malformed identity snapshot accepted")
		}
		after, err := rows.agentIdentity(ctx, crewProfileID, mainKey)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("failed snapshot replaced identity bindings", err)
		}
	})
}

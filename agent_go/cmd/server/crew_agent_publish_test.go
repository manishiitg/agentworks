package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// PLAT-856: disabled programs cannot be added from Providers. Only an enabled
// program missing an own account should offer that sign-in action.
func TestPublishAgentPreflightDistinguishesDisabledProgramFromMissingLogin(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("PUBLIC_URL", "https://agents.example.com")
	for _, tc := range []struct {
		name, enabled, provider string
		login                   bool
		alternative             string
	}{
		{"disabled with portable alternative", "muse-cli", "claude-code", false, "Muse"},
		{"enabled without own login", "muse-cli", "muse-cli", true, ""},
		{"no portable alternative", "pi-cli", "claude-code", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUPPORTED_LLM_PROVIDERS", tc.enabled)
			preview, err := env.api.crewAgentPublishAccounts(t.Context(), &UserClaims{UserID: "alice"}, crewroot.Roots{}, tc.provider, "")
			if err != nil {
				t.Fatal(err)
			}
			if preview.Ready || preview.NeedsLogin != tc.login || len(preview.Accounts) != 0 {
				t.Fatalf("wrong setup action: %+v", preview)
			}
			if !strings.Contains(preview.Message, providerAccountProductName(tc.provider)) || !strings.Contains(preview.Message, "agents.example.com") {
				t.Fatalf("setup message does not identify the program and destination: %s", preview.Message)
			}
			if tc.alternative != "" && !strings.Contains(preview.Message, tc.alternative) {
				t.Fatalf("missing available program: %s", preview.Message)
			}
			if !tc.login && strings.Contains(preview.Message, "Providers") || strings.Contains(preview.Message, "Pi") {
				t.Fatalf("offered an unavailable setup path: %s", preview.Message)
			}
		})
	}
}

// PLAT-856-523: selecting one agent cannot move another tab's callers, and
// merging its callers must retain destination schedules that already exist.
func TestPublishAgentKeepsOtherAgentsCallers(t *testing.T) {
	h := crewMoveHeader{SourceID: "crew", MainKey: "crew", AgentTab: "writer", Schedules: []projectactions.Schedule{
		{ID: "main", Action: projectactions.Action{Type: "message"}},
		{ID: "selected", Action: projectactions.Action{Type: "message", Tab: "crew:chat:writer"}},
		{ID: "other", Action: projectactions.Action{Type: "message", Tab: "crew:chat:reader"}},
	}, Watchers: map[string]crewMoveWatcherState{"main": {}, "selected": {}, "other": {}}}
	if err := filterCrewAgentCallers(&h); err != nil {
		t.Fatal(err)
	}
	if len(h.Schedules) != 1 || h.Schedules[0].ID != "selected" || len(h.Watchers) != 1 {
		t.Fatalf("moved another agent's callers: %+v", h)
	}
	old := []projectactions.Schedule{{ID: "already-online", Enabled: true}, {ID: "selected"}}
	h.Schedules[0].Enabled = true
	merged := mergeCrewPublishedItems(old, h.Schedules, func(s projectactions.Schedule) string { return s.ID })
	if len(merged) != 2 || merged[0].ID != "already-online" || !merged[0].Enabled || !merged[1].Enabled {
		t.Fatalf("lost or duplicated a destination schedule: %+v", merged)
	}
	// A later Publish Crew must not replace the live server agent with the
	// disabled copy left on its Mac.
	h.Schedules = []projectactions.Schedule{{ID: "moved", Action: projectactions.Action{Type: "message", Tab: "crew:chat:writer"}}, {ID: "remaining", Action: projectactions.Action{Type: "message", Tab: "crew:chat:reader"}}}
	h.AgentTab = ""
	excludePublishedCrewCallers(&h, map[string]crewAgentPlacement{"writer": {Tab: "writer", Location: "server"}})
	if len(h.Schedules) != 1 || h.Schedules[0].ID != "remaining" {
		t.Fatalf("re-imported retired callers: %+v", h.Schedules)
	}
}

func TestPublishAgentFileSelectionCannotEscapeCrew(t *testing.T) {
	for _, p := range []string{"../private", "/etc", "src/../private", "a\\b", "a\x00b"} {
		if _, err := cleanCrewAgentPublishSelection(crewAgentPublishSelection{Tab: "writer", Paths: []string{p}}); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	selection, err := cleanCrewAgentPublishSelection(crewAgentPublishSelection{Tab: "writer", Paths: []string{"src/app"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		key             string
		directory, want bool
	}{
		{"files/src", true, true}, {"files/src/app/main.go", false, true},
		{"files/src/other.go", false, false}, {"resources/skills/writer/SKILL.md", false, true},
		{"resources/MEMORY.md", false, true}, {"knowledge/knowledgebase/private.md", false, false},
		{"resources/published-agents/other/MEMORY.md", false, false},
	} {
		if got := crewAgentPublishIncludes(selection, test.key, test.directory); got != test.want {
			t.Errorf("%s included=%t want=%t", test.key, got, test.want)
		}
	}
}

func TestPublishAgentRefusesArchiveInsteadOfClaimingNativeResume(t *testing.T) {
	api := &StreamingAPI{}
	err := api.importCrewHandoffConversation(context.Background(), &UserClaims{UserID: "owner"}, crewroot.Roots{}, "pair", "transfer", &crewHandoffConversation{ConversationID: "conversation-test"}, ProductConversationRecord{}, crewImportNative)
	if err == nil {
		t.Fatal("native publish accepted an archive without its native session")
	}
}

// Exercise the real content bundle and receiver, including private-file rules,
// rather than treating a selected-path predicate as transfer proof.
func TestPublishAgentContentBundleKeepsOtherAgentAndPrivateFiles(t *testing.T) {
	root := t.TempDir()
	makeRoot := func(name string) crewroot.Roots {
		files, private := filepath.Join(root, name, "files"), filepath.Join(root, name, "private")
		for _, dir := range []string{files, private} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return crewroot.Roots{ID: name, Owner: "owner", Virtual: "Crew/" + name, Files: files, Resources: files, Private: private}
	}
	write := func(root, name, content string) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source, destination := makeRoot("source"), makeRoot("destination")
	for name, data := range map[string]string{"reports/result.md": "selected work", "other/work.md": "unselected", "skills/report/SKILL.md": "skill", "MEMORY.md": "memory", ".env": "not transferable", "node_modules/package/index.js": "dependency", "published-agents/other/work.md": "other agent"} {
		write(source.Resources, name, data)
	}
	write(source.Private, "builder/conversation/private.json", "private conversation")
	write(destination.Resources, "published-agents/other/keep.md", "other agent stays")
	write(destination.Private, "product.json", "platform settings stay")
	selection := crewAgentPublishSelection{Tab: "writer", Paths: []string{"reports"}}
	snapshot, err := crewsync.CaptureSelected(t.Context(), source, nil, source.Private, func(key string, dir bool) bool { return crewAgentPublishIncludes(selection, key, dir) })
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err = (filesnapshots.Store{Root: source.Files, Private: source.Private}).EncodeSync(t.Context(), "pair-agent", source, &snapshot); err != nil {
		t.Fatal(err)
	}
	scoped, err := crewAgentScopedRoots(destination, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if err = applyCrewSyncSnapshot(t.Context(), scoped, "pair-agent", &snapshot); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"reports/result.md": "selected work", "skills/report/SKILL.md": "skill", "MEMORY.md": "memory"} {
		data, err := os.ReadFile(filepath.Join(scoped.Resources, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s: %q %v", name, data, err)
		}
	}
	for _, name := range []string{"other/work.md", ".env", "node_modules/package/index.js", "published-agents/other/work.md", "builder/conversation/private.json"} {
		if _, err := os.Stat(filepath.Join(scoped.Resources, name)); !os.IsNotExist(err) {
			t.Fatalf("transferred excluded path %s: %v", name, err)
		}
	}
	for p, want := range map[string]string{filepath.Join(destination.Resources, "published-agents/other/keep.md"): "other agent stays", filepath.Join(destination.Private, "product.json"): "platform settings stay"} {
		data, err := os.ReadFile(p)
		if err != nil || string(data) != want {
			t.Fatalf("changed unrelated state: %s %q %v", p, data, err)
		}
	}
}

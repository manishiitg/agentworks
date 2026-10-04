package slots

import (
	"path/filepath"
	"testing"
)

// identityEnv is a host with two slot users and the docs tree the products launch in (PLAT-442 regression table,
// workspace half): the same rows the provider's slotfs table pins.
func identityEnv(t *testing.T) (cfg ExecConfig, docs string) {
	t.Helper()
	root := t.TempDir()
	table := writeTable(t, `{"slots":{"slot08":"user-a","slot09":"user-b"}}`)
	return ExecConfig{
		DocsRoot:      filepath.Join(root, "docs"),
		SlotTable:     table,
		SlotStateRoot: filepath.Join(root, "state"),
		SlotRunRoot:   filepath.Join(root, "run"),
	}, filepath.Join(root, "docs")
}

// TestSlotForDirRegressionTable pins what the folder rule gives for each product's folder. It passed unchanged on
// the code before PLAT-442 step 3 migrated SlotForDir onto workspaceref.
func TestSlotForDirRegressionTable(t *testing.T) {
	cfg, docs := identityEnv(t)
	for _, tc := range []struct {
		name, dir, want string
	}{
		{"Code, A's project", filepath.Join(docs, "_users", "user-a", "Chats", "Code", "projects", "app-1"), "slot08"},
		{"Code, a file in it", filepath.Join(docs, "_users", "user-a", "Chats", "Code", "projects", "app-1", "code"), "slot08"},
		{"Crew, A's project (owner's turn)", filepath.Join(docs, "_users", "user-a", "Chats", "Work", "projects", "crew-1"), "slot08"},
		{"Crew, B reading A's project: A's folder", filepath.Join(docs, "_users", "user-a", "Chats", "Work", "projects", "crew-1"), "slot08"},
		{"B's own tree", filepath.Join(docs, "_users", "user-b", "Chats", "Work", "projects", "crew-2"), "slot09"},
		{"Goal", filepath.Join(docs, "Workflow", "goal-1"), ""},
		{"Goal run", filepath.Join(docs, "Workflow", "goal-1", "runs", "iteration-0"), ""},
		{"a user without a slot", filepath.Join(docs, "_users", "user-c", "Chats"), ""},
		{"the users folder itself", filepath.Join(docs, "_users"), ""},
		{"a user folder, nothing below", filepath.Join(docs, "_users", "user-a"), "slot08"},
		{"outside the docs root", filepath.Join(filepath.Dir(docs), "state-elsewhere", "_users", "user-a"), ""},
		{"CLI runtime folder (app state root)", filepath.Join(filepath.Dir(docs), "app", "state", "cli-runtimes", "v1", "abc"), ""},
		{"a slot's own state folder", filepath.Join(cfg.SlotStateRoot, "slot08", "cli"), "slot08"},
		{"a slot's own run folder", filepath.Join(cfg.SlotRunRoot, "slot09", "f"), "slot09"},
		{"not a slot name under the state root", filepath.Join(cfg.SlotStateRoot, "notaslot", "x"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfg.SlotForDir(tc.dir); got != tc.want {
				t.Fatalf("SlotForDir(%q) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}
}

func TestIsCodeProjectDirRegressionTable(t *testing.T) {
	docs := filepath.Join(t.TempDir(), "docs")
	for _, tc := range []struct {
		name string
		dir  string
		want bool
	}{
		{"Code project root", filepath.Join(docs, "_users", "u", "Chats", "Code", "projects", "p"), true},
		{"inside a Code project", filepath.Join(docs, "_users", "u", "Chats", "Code", "projects", "p", "code", "src"), true},
		{"the projects folder itself", filepath.Join(docs, "_users", "u", "Chats", "Code", "projects"), true},
		{"Code folder without projects", filepath.Join(docs, "_users", "u", "Chats", "Code"), false},
		{"a Crew project", filepath.Join(docs, "_users", "u", "Chats", "Work", "projects", "p"), false},
		{"a Goal", filepath.Join(docs, "Workflow", "g"), false},
		{"no owner segment", filepath.Join(docs, "Chats", "Code", "projects", "p"), false},
		{"outside the docs root", filepath.Join(filepath.Dir(docs), "elsewhere", "_users", "u", "Chats", "Code", "projects", "p"), false},
		{"another user's Code (the answer does not depend on who asks)", filepath.Join(docs, "_users", "other", "Chats", "Code", "projects", "p"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCodeProjectDir(docs, tc.dir); got != tc.want {
				t.Fatalf("IsCodeProjectDir(%q) = %v, want %v", tc.dir, got, tc.want)
			}
		})
	}
}

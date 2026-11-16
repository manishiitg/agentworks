package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// #2867: stale tools-only and full packages loaded together; setup must update
// the kept package at each actual scope before removing only its other variant.
func TestClaudeSetupKeepsOneUpdatedPluginPerScope(t *testing.T) {
	for _, tc := range []struct{ name, want, fail string }{
		{"hooks", claudeFullPlugin, ""},
		{"tools_only", claudeToolsPlugin, ""},
		{"project_hooks_disabled", claudeFullPlugin, ""},
		{"update_fails", claudeFullPlugin, "update"},
		{"uninstall_fails", claudeFullPlugin, "uninstall"},
		{"install_not_registered", "missing@agentworks", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"plugins":{
    "agentworks@agentworks":[{"scope":"user","version":"1"},{"scope":"project","projectPath":"/projects/example","version":"2"}],
    "agentworks-tools@agentworks":[{"scope":"user","version":"1"},{"scope":"project","projectPath":"/projects/example","version":"1"}],
    "other@market":[{"scope":"user"}]
   }}`)
			var calls []string
			var receipts []claudePluginCleanup
			err := reconcileClaudePlugins(raw, tc.want, "user", func(entry claudePluginScope) (string, error) {
				if tc.name == "project_hooks_disabled" && entry.Scope == "project" {
					return claudeToolsPlugin, nil
				}
				return "", nil
			}, func(dir string, args ...string) error {
				calls = append(calls, dir+"|"+strings.Join(args, " "))
				if len(args) > 1 && args[1] == tc.fail {
					return errors.New("fixture provider failure")
				}
				return nil
			}, func(r claudePluginCleanup) { receipts = append(receipts, r) })
			if tc.fail != "" || tc.name == "install_not_registered" {
				if err == nil {
					t.Fatal("failed provider operation reported success")
				}
				if tc.fail == "update" {
					for _, call := range calls {
						if strings.Contains(call, "uninstall") {
							t.Fatal("removed the old plugin before replacement update succeeded")
						}
					}
				}
				if tc.name == "install_not_registered" && len(calls) != 0 {
					t.Fatal("changed plugins without a registered replacement")
				}
				if tc.fail == "uninstall" && (len(receipts) != 1 || receipts[0].Outcome != "failed" || receipts[0].Removed != "none") {
					t.Fatal("failed removal reported as complete")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			other := claudeToolsPlugin
			if tc.want == claudeToolsPlugin {
				other = claudeFullPlugin
			}
			projectKeep, projectOther := claudeFullPlugin, claudeToolsPlugin
			if tc.name == "project_hooks_disabled" {
				projectKeep, projectOther = claudeToolsPlugin, claudeFullPlugin
			}
			want := []string{
				"|plugin marketplace update agentworks",
				"/projects/example|plugin update " + projectKeep + " --scope project",
				"/projects/example|plugin uninstall " + projectOther + " --scope project --keep-data",
				"|plugin update " + tc.want + " --scope user",
				"|plugin uninstall " + other + " --scope user --keep-data",
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("unexpected scope/variant operations: %v", calls)
			}
			if len(receipts) != 2 || receipts[0].Removed != projectOther || receipts[1].Removed != other {
				t.Fatal("cleanup receipt lost the actual variant")
			}
		})
	}
}

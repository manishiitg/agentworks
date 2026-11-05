package server

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// All in Vault (PLAT-856, reviewer must-have): after the startup move, a Goal run by an editor or by a schedule, a
// Flow run by a schedule and a Crew owner's turn still get the moved secret, lent by the project's owner, through the
// same resolution the runs use. The move keeps a same-valued secret, renames a clashing one, backs up first, logs
// names only and has nothing left to do the second time.
func TestProjectVaultMoveKeepsOwnerLentRuns(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"e2","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]}]}`)
	vault := newOwnerVaultFake(t)
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.api.chatStore = store
	ctx := context.Background()
	const goal, flow = "Workflow/renewals", "Workflow/intake"
	f.write(t, goal+"/workflow.json", `{"id":"renewals","label":"Renewals","created_by":"owner","access":{"owners":["owner"],"editors":["e2"]},"capabilities":{"selected_secrets":["TOKEN"],"selected_global_secret_names":["TOKEN"]}}`)
	f.write(t, flow+"/workflow.json", `{"id":"intake","label":"Intake","kind":"relay","created_by":"owner","access":{"owners":["owner"]},"capabilities":{"selected_secrets":["TOKEN"]}}`)
	f.write(t, externalCommandCrewState+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner","capabilities":{"selected_secrets":["TOKEN","API_KEY"]}}`)
	registerWorkflowOwner(goal, "owner")
	registerWorkflowOwner(flow, "owner")
	// The owner's own Vault already holds TOKEN with the Goal's value; the Flow and the Crew hold different ones.
	vault.seed(t, f.api, "owner", "", "TOKEN", "goal-value-1")
	old := []string{
		vault.seed(t, f.api, "owner", goal, "TOKEN", "goal-value-1"),
		vault.seed(t, f.api, "owner", flow, "TOKEN", "flow-value-2"),
		vault.seed(t, f.api, "owner", externalCommandCrewRoot, "TOKEN", "crew-value-3"),
		vault.seed(t, f.api, "owner", externalCommandCrewRoot, "API_KEY", "crew-key-4"),
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	if retry, err := f.api.moveProjectVaultSecrets(ctx); err != nil || retry {
		t.Fatalf("move: retry=%v err=%v\n%s", retry, err, logs.String())
	}
	for _, name := range old {
		managedGlobalsMu.RLock()
		_, kept := managedGlobals[name]
		managedGlobalsMu.RUnlock()
		if kept || vault.has(name) {
			t.Fatalf("the project copy %s was not removed", name)
		}
	}
	for _, want := range []string{
		"project=" + goal + " secret=TOKEN to=TOKEN result=selected_existing",
		"project=" + flow + " secret=TOKEN to=INTAKE_TOKEN result=renamed",
		"project=" + externalCommandCrewRoot + " secret=TOKEN to=TEST_TEAM_TOKEN result=renamed",
		"project=" + externalCommandCrewRoot + " secret=API_KEY to=API_KEY result=moved",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("missing %q in\n%s", want, logs.String())
		}
	}
	for _, value := range []string{"goal-value-1", "flow-value-2", "crew-value-3", "crew-key-4"} {
		if strings.Contains(logs.String(), value) {
			t.Fatal("a secret value reached the log")
		}
	}
	backups, _ := filepath.Glob(filepath.Join(os.Getenv("AGENTWORKS_STATE_ROOT"), "vault-migration-backups", "*", "values.json"))
	if len(backups) != 1 {
		t.Fatalf("backup files: %v", backups)
	}
	if info, err := os.Stat(backups[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup is not private: %v %v", info, err)
	}

	value := func(rows []resolvedSecret, name string) string {
		for _, row := range rows {
			if row.Name == name {
				return row.Value
			}
		}
		return ""
	}
	run := func(person, workspace string) []resolvedSecret {
		t.Helper()
		manifest, found, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil || !found {
			t.Fatal("manifest unavailable", err)
		}
		if raw, _, _ := readFileFromWorkspace(ctx, workspace+"/workflow.json"); strings.Contains(raw, "selected_secrets") {
			t.Fatalf("%s still lists the retired project selection", workspace)
		}
		rows, err := f.api.projectRunSecrets(ctx, person, workspace, manifest.Capabilities.SelectedGlobalSecretNames)
		if err != nil {
			t.Fatalf("%s run as %s refused: %v", workspace, person, err)
		}
		return rows
	}
	schedule := func(workspace, runAs string) []resolvedSecret {
		t.Helper()
		manifest, _, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		sctx := buildScheduleContext(workspace, manifest, WorkflowSchedule{RunAs: runAs})
		rows, err := f.api.projectRunSecrets(ctx, sctx.OwnerUserID, sctx.WorkspacePath, sctx.Capabilities.SelectedGlobalSecretNames)
		if err != nil {
			t.Fatalf("%s schedule (run as %q) refused: %v", workspace, runAs, err)
		}
		return rows
	}
	if got := value(run("e2", goal), "TOKEN"); got != "goal-value-1" {
		t.Fatal("a Goal run by an editor lost its secret")
	}
	for _, runAs := range []string{"", "e2"} {
		if got := value(schedule(goal, runAs), "TOKEN"); got != "goal-value-1" {
			t.Fatalf("a Goal schedule (run as %q) lost its secret", runAs)
		}
		if got := value(schedule(flow, runAs), "INTAKE_TOKEN"); got != "flow-value-2" {
			t.Fatalf("a Flow schedule (run as %q) lost its secret", runAs)
		}
	}
	env := f.api.crewSelectedSecretEnv(ctx, crewProfileID, externalCommandCrewRoot, externalCommandCrewRoot, "owner")
	if env["SECRET_TEST_TEAM_TOKEN"] != "crew-value-3" || env["SECRET_API_KEY"] != "crew-key-4" || len(env) != 2 {
		names := []string{}
		for name := range env {
			names = append(names, name)
		}
		selected, _ := productSelectedGlobalSecrets(ctx, crewProfileID, externalCommandCrewRoot)
		t.Fatalf("the Crew owner's turn lost its secrets: got %v, selection %v", names, *selected)
	}
	// Someone the owner did not lend it to does not get it through their own grants.
	if err := validateVaultSecretSelection(ctx, "other", &[]string{vault.vaultName(t, "owner", "API_KEY")}); err == nil {
		t.Fatal("the moved secret widened another person's own grants")
	}

	logs.Reset()
	if retry, err := f.api.moveProjectVaultSecrets(ctx); err != nil || retry || strings.Contains(logs.String(), "[VAULT_MIGRATE]") {
		t.Fatalf("a second move found work: retry=%v err=%v\n%s", retry, err, logs.String())
	}
}

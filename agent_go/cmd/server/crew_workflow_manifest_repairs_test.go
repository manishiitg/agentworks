package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// PLAT-746-396: a target can move after the workflow was stamped. The named
// repair must work at 1.0.52 without retargeting or granting another owner's Crew.
func TestWorkflowAttachmentRepairAfterStampKeepsTargetAndRefusesForeignOrMissing(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	svc, files, ctx := newCrewCreationTestEnv(t)
	target, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Source", Purpose: "Reports", IdempotencyKey: "repair-source"})
	if err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Report")
	manifest.ID, manifest.Version, manifest.CodeLayoutVersion = "repair-report", workflowContractPortableVersion, 1
	manifest.CrewAttachments = []workflowtypes.CrewAttachment{{ID: "attachment", Alias: "source", CrewProfileID: "work", CrewProjectID: target.CrewID, CrewWorkspacePath: "_users/owner/Chats/Work/projects/" + strings.TrimPrefix(target.WorkspacePath, "Crew/"), CreatedAt: "original"}}
	const root = "Workflow/repair-report"
	raw, _ := json.Marshal(manifest)
	files.files[root+"/workflow.json"] = string(raw)
	if err := preflightCrewAttachments(ctx, svc, "owner", root); err == nil {
		t.Fatal("stale root ran before repair")
	}
	if _, err := repairWorkflowManifest(ctx, root, manifest, true, false, "Refresh the same project after its move"); err != nil {
		t.Fatal(err)
	}
	saved, found, err := ReadWorkflowManifest(ctx, root)
	if err != nil || !found {
		t.Fatal(err)
	}
	attachment := saved.CrewAttachments[0]
	if saved.Version != workflowContractPortableVersion || attachment.CrewProjectID != target.CrewID || attachment.ID != "attachment" || attachment.Alias != "source" || attachment.CreatedAt != "original" || attachment.CrewWorkspacePath != target.WorkspacePath {
		t.Fatalf("repair changed identity/version: %+v", saved)
	}
	if err := preflightCrewAttachments(ctx, svc, "owner", root); err != nil {
		t.Fatalf("repaired attachment refused: %v", err)
	}
	identity, _ := projectIdentityOf(target.WorkspacePath)
	for _, problem := range []string{"other owner", "missing"} {
		if err := defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
			key := projectOwnerKey(identity.Product, identity.Folder)
			if problem == "missing" {
				delete(projects, key)
			} else {
				record := projects[key]
				record.OwnerID = "other"
				projects[key] = record
			}
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		before := files.files[root+"/workflow.json"]
		_, err := repairWorkflowManifest(ctx, root, saved, true, false, "Refresh only the same owned project")
		want := "do not own"
		if problem == "missing" {
			want = "missing or ambiguous"
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s refused for wrong reason: %v", problem, err)
		}
		if files.files[root+"/workflow.json"] != before {
			t.Fatal("refused repair changed or deleted the connection")
		}
	}
}

func TestWorkflowBrowserRepairUsesExistingDeploymentPolicy(t *testing.T) {
	for _, deployment := range []string{"local", "server"} {
		t.Run(deployment, func(t *testing.T) {
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", deployment)
			want := "headless"
			if deployment == "local" {
				want = "extension"
			}
			if mode, changed := workflowBrowserModeRepair("auto"); !changed || mode != want {
				t.Fatalf("auto mapped to %q, changed=%v", mode, changed)
			}
			for _, mode := range []string{"none", "extension", ""} {
				if got, changed := workflowBrowserModeRepair(mode); changed || got != mode {
					t.Fatalf("explicit %q changed to %q", mode, got)
				}
			}
		})
	}
}

package workspace

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// The PLAT-280 log line fires only for a session granted direct DB access
// whose command still lacks DB_PATH; agentic steps, denied raw access by
// design, never trip it.
func TestPLAT280DiagnosticFiresOnlyForAGrantedMissingPath(t *testing.T) {
	agentic := &common.SessionShellConfig{BlockedPaths: []string{"Workflow/rtsaws/db/db.sqlite", "Workflow/rtsaws/db/db.sqlite-wal"}}
	scripted := &common.SessionShellConfig{}
	withAccess := map[string]string{"WORKFLOW_DB_ACCESS": "read-write"}

	if shellMissingGrantedDBPath(withAccess, agentic) {
		t.Error("an agentic step without DB_PATH is by design, not an anomaly")
	}
	if !shellMissingGrantedDBPath(withAccess, scripted) {
		t.Error("a scripted step granted DB access but missing DB_PATH must be reported")
	}
	if shellMissingGrantedDBPath(map[string]string{"WORKFLOW_DB_ACCESS": "read-write", "DB_PATH": "/docs/Workflow/x/db/db.sqlite"}, scripted) {
		t.Error("a scripted step that has DB_PATH is fine")
	}
	if shellMissingGrantedDBPath(map[string]string{}, scripted) {
		t.Error("a session without DB access is not in scope")
	}
}

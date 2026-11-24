package server

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// One-off cleanup after the personal AgentWorks project was retired (#3692): the platform no longer makes one, and
// the owner asked that the existing ones go too, without deleting each by hand. On every start, each person's old
// AgentWorks project (the one the platform made under its reserved id) is backed up (crew-backups/<id>-<time>.tar.gz)
// and deleted through the ordinary project deletion. One that is working or used by a workflow is kept and tried
// again on the next start. One its person has chatted in is kept for good (owner, 24 Nov: delete only the unused
// ones; a used one is deleted by its owner like any other project). Remove this file once every server has run it.

// retiredAgentWorksProjectID is the id the platform reserved for a person's AgentWorks project.
func retiredAgentWorksProjectID(owner string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("agentworks\x00"+strings.TrimSpace(owner))).String()
}

func (api *StreamingAPI) deleteRetiredAgentWorksProjects(ctx context.Context) {
	records, err := defaultProjectOwners().All()
	if err != nil {
		log.Printf("[AGENTWORKS_CLEANUP] event=skipped reason=%q", err.Error())
		return
	}
	for _, record := range records {
		if record.Product != crewProfileID || record.OwnerID == "" || record.ProjectID != retiredAgentWorksProjectID(record.OwnerID) {
			continue
		}
		if agentWorksProjectUsed(record.ProjectID) {
			log.Printf("[AGENTWORKS_CLEANUP] project=%s owner=%s event=kept reason=%q", record.ProjectID, logUsernameForUserID(record.OwnerID), "used: it has chats")
			continue
		}
		backup, err := api.deleteRetiredAgentWorksProject(ctx, record)
		if err != nil {
			log.Printf("[AGENTWORKS_CLEANUP] project=%s owner=%s event=kept reason=%q backup=%s", record.ProjectID, logUsernameForUserID(record.OwnerID), err.Error(), backup)
			continue
		}
		log.Printf("[AGENTWORKS_CLEANUP] project=%s owner=%s event=deleted backup=%s", record.ProjectID, logUsernameForUserID(record.OwnerID), backup)
	}
}

// agentWorksProjectUsed reports whether anyone has chatted in the project: its chat data folder holds a file.
func agentWorksProjectUsed(project string) bool {
	used := false
	_ = filepath.WalkDir(filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "crew-data", project, "builder"), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			used = true
			return fs.SkipAll
		}
		return nil
	})
	return used
}

func (api *StreamingAPI) deleteRetiredAgentWorksProject(ctx context.Context, record projectOwnerRecord) (string, error) {
	roots, found, err := crewStorageRoots(firstNonEmptyTrimmed(record.CanonicalRoot, workspaceref.SharedProjectPath(record.Folder)))
	if err != nil || !found || roots.Owner != record.OwnerID {
		return "", fmt.Errorf("its folder could not be found for a backup")
	}
	backup, err := backupCrewFolders(roots)
	if err != nil {
		return "", err
	}
	if t, err := readCrewTeam(record.ProjectID); err == nil && len(t.People) > 0 {
		log.Printf("[AGENTWORKS_CLEANUP] project=%s event=team_ends people=%d", record.ProjectID, len(t.People))
	}
	claims := &UserClaims{UserID: record.OwnerID, Username: record.OwnerID}
	req := httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, claims))
	status, body := scheduleHandler(req, api.handleDeleteAgentProfileProject, http.MethodDelete, "/", map[string]string{"id": crewProfileID, "project_id": record.ProjectID}, nil, nil)
	if status >= 300 {
		if status == http.StatusConflict {
			// Refused before anything was removed (working, or used by a workflow): the next start backs it up again.
			_ = os.Remove(backup)
			backup = ""
		}
		return backup, fmt.Errorf("delete answered %d: %s", status, strings.TrimSpace(string(body)))
	}
	return backup, nil
}

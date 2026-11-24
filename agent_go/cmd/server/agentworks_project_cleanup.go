package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// One-off cleanup after the personal AgentWorks project was retired (#3692): the platform no longer makes one, and
// the owner asked that the existing ones go too, without deleting each by hand. On every start, each person's old
// AgentWorks project (the one the platform made under its reserved id) is backed up (crew-backups/<id>-<time>.tar.gz)
// and deleted through the ordinary project deletion. One that is working or used by a workflow is kept and tried
// again on the next start. Remove this file once every server has run it.

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
		backup, err := api.deleteRetiredAgentWorksProject(ctx, record)
		if err != nil {
			log.Printf("[AGENTWORKS_CLEANUP] project=%s owner=%s event=kept reason=%q", record.ProjectID, logUsernameForUserID(record.OwnerID), err.Error())
			continue
		}
		log.Printf("[AGENTWORKS_CLEANUP] project=%s owner=%s event=deleted backup=%s", record.ProjectID, logUsernameForUserID(record.OwnerID), backup)
	}
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
	claims := &UserClaims{UserID: record.OwnerID, Username: record.OwnerID}
	req := httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, claims))
	status, body := scheduleHandler(req, api.handleDeleteAgentProfileProject, http.MethodDelete, "/", map[string]string{"id": crewProfileID, "project_id": record.ProjectID}, nil, nil)
	if status >= 300 {
		return backup, fmt.Errorf("delete answered %d: %s", status, strings.TrimSpace(string(body)))
	}
	return backup, nil
}

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
	"time"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// One-off cleanup after the personal AgentWorks project was retired (#3692): the platform no longer makes one, and
// the owner asked that the existing ones go too, without deleting each by hand. On every start, each person's old
// AgentWorks project (the one the platform made under its reserved id) is backed up (crew-backups/<id>-<time>.tar.gz)
// and deleted through the ordinary project deletion. One that is working or used by a workflow is kept and tried
// again on the next start. A project in use (a chat in the last 30 days, or on a server apps or keys of its own in its
// project vault) is kept as an ordinary one. Remove this file once every server has run it.

// retiredAgentWorksProjectID is the id the platform reserved for a person's AgentWorks project.
func retiredAgentWorksProjectID(owner string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("agentworks\x00"+strings.TrimSpace(owner))).String()
}

func (api *StreamingAPI) deleteRetiredAgentWorksProjects(ctx context.Context) {
	// After start-up, so the Vault service answers the in-use check below.
	select {
	case <-ctx.Done():
		return
	case <-time.After(crewVaultQueueStartDelay):
	}
	records, err := defaultProjectOwners().All()
	if err != nil {
		log.Printf("[AGENTWORKS_CLEANUP] event=skipped reason=%q", err.Error())
		return
	}
	for _, record := range records {
		if record.Product != crewProfileID || record.OwnerID == "" || record.ProjectID != retiredAgentWorksProjectID(record.OwnerID) {
			continue
		}
		if reason := api.retiredAgentWorksProjectInUse(ctx, record); reason != "" {
			log.Printf("[AGENTWORKS_CLEANUP] project=%s owner=%s event=kept_in_use reason=%q", record.ProjectID, logUsernameForUserID(record.OwnerID), reason)
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

// retiredAgentWorksProjectInUse keeps a project someone uses (owner, 24 Nov: "keep used ones"): one with a chat in
// the last 30 days, or, on a server, one whose project vault holds apps or keys of its own. It stays as an ordinary
// project. Workflows that use it already stop the delete.
func (api *StreamingAPI) retiredAgentWorksProjectInUse(ctx context.Context, record projectOwnerRecord) string {
	roots, found, err := crewStorageRoots(retiredAgentWorksProjectRoot(record))
	if err != nil || !found {
		return "its folder could not be found"
	}
	if last := lastProjectChatAt(roots.Private); time.Since(last) < retiredAgentWorksIdle {
		return "it has a chat from " + last.UTC().Format(time.DateOnly)
	}
	if IsLocalMode() {
		return ""
	}
	contents, err := api.readCrewVault(ctx, record.ProjectID)
	if err != nil {
		return "its apps and keys could not be checked: " + err.Error()
	}
	if len(contents.Apps) > 0 || len(contents.SecretNames) > 0 {
		return fmt.Sprintf("it has %d apps and %d keys of its own", len(contents.Apps), len(contents.SecretNames))
	}
	return ""
}

const retiredAgentWorksIdle = 30 * 24 * time.Hour

func retiredAgentWorksProjectRoot(record projectOwnerRecord) string {
	return firstNonEmptyTrimmed(record.CanonicalRoot, workspaceref.SharedProjectPath(record.Folder))
}

// lastProjectChatAt is when any chat of the project last changed: its saved conversations live in its private data
// folder (<id>/builder/conversation/<date>/session-…-conversation.json and the same for every tab).
func lastProjectChatAt(private string) time.Time {
	var last time.Time
	if private == "" {
		return last
	}
	_ = filepath.WalkDir(private, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "file-snapshots" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "-conversation.json") {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
		return nil
	})
	return last
}

func (api *StreamingAPI) deleteRetiredAgentWorksProject(ctx context.Context, record projectOwnerRecord) (string, error) {
	roots, found, err := crewStorageRoots(retiredAgentWorksProjectRoot(record))
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

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// remoteProjectLink links a project to one on another machine (#3663): a Mac project to a project on a server it is
// signed in to, and that server project back to the Mac project. Both sides save it, so agents on either side can list
// and message each other's agents. It grants no file access: the Mac reads server files with server_project_files and
// the server reads Mac files through its Local laptop connection.
type remoteProjectLink struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name,omitempty"`
	// Server is the Mac's saved server sign-in (its ID). Empty on the server, where the link names a Mac project.
	Server string `json:"server,omitempty"`
}

// remoteProjectLinkArg is the project_id of a link to the other machine, or "" for an ordinary workspace_path reference.
// The tool and the server's external gate both decide with it, so a blank ID never skips the reference check.
func remoteProjectLinkArg(args map[string]any) (string, error) {
	project, _ := args["project_id"].(string)
	project = strings.TrimSpace(project)
	if project == "" {
		return "", nil
	}
	if path, _ := args["workspace_path"].(string); strings.TrimSpace(path) != "" {
		return "", fmt.Errorf("pass project_id or workspace_path, not both")
	}
	if len(project) > 128 || strings.IndexFunc(project, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return "", fmt.Errorf("project_id is not a project ID")
	}
	return project, nil
}

func readRemoteProjectLinks(ctx context.Context, workspacePath string) ([]remoteProjectLink, error) {
	raw, found, err := readProjectRuntimeManifest(ctx, "work", workspacePath)
	if err != nil || !found {
		return nil, err
	}
	var manifest struct {
		Links []remoteProjectLink `json:"linked_remote_projects"`
	}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, fmt.Errorf("decode Work runtime manifest: %w", err)
	}
	return manifest.Links, nil
}

func updateRemoteProjectLinks(ctx context.Context, workspacePath string, mutate func([]remoteProjectLink) ([]remoteProjectLink, error)) ([]remoteProjectLink, error) {
	manifestPath := projectRuntimeManifestPath("work", workspacePath)
	mutex := productConversationRegistryMutex(manifestPath)
	mutex.Lock()
	defer mutex.Unlock()
	raw, _, err := ensureProjectRuntimeManifest(ctx, "work", workspacePath)
	if err != nil {
		return nil, err
	}
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil, fmt.Errorf("decode Work runtime manifest: %w", err)
	}
	var current struct {
		Links []remoteProjectLink `json:"linked_remote_projects"`
	}
	_ = json.Unmarshal([]byte(raw), &current)
	next, err := mutate(current.Links)
	if err != nil {
		return nil, err
	}
	if len(next) == 0 {
		delete(document, "linked_remote_projects")
	} else {
		document["linked_remote_projects"] = next
	}
	document["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return next, writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}

// setRemoteProjectLink adds or renames the link to project on server.
func setRemoteProjectLink(ctx context.Context, workspacePath string, link remoteProjectLink) ([]remoteProjectLink, error) {
	return updateRemoteProjectLinks(ctx, workspacePath, func(links []remoteProjectLink) ([]remoteProjectLink, error) {
		for i, l := range links {
			if l.ProjectID == link.ProjectID && l.Server == link.Server {
				links[i] = link
				return links, nil
			}
		}
		return append(links, link), nil
	})
}

// removeRemoteProjectLink removes the link to project and returns the removed entries.
func removeRemoteProjectLink(ctx context.Context, workspacePath, project string) (removed []remoteProjectLink, err error) {
	_, err = updateRemoteProjectLinks(ctx, workspacePath, func(links []remoteProjectLink) ([]remoteProjectLink, error) {
		kept := links[:0]
		for _, l := range links {
			if l.ProjectID == project {
				removed = append(removed, l)
				continue
			}
			kept = append(kept, l)
		}
		if len(removed) == 0 {
			return nil, fmt.Errorf("project %s is not linked", project)
		}
		return kept, nil
	})
	return removed, err
}

// linkRemoteProject saves a link to a project on the other machine. On the Mac it links a project on a server this Mac
// is signed in to, and asks that server to save the link back (the server checks the person may change that project).
// On the server it saves the link back to the Mac project, which only that call makes.
func linkRemoteProject(ctx context.Context, userID, workspacePath, server, project, name string) (map[string]any, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("give the project ID to link")
	}
	if !IsLocalMode() {
		if strings.TrimSpace(server) != "" {
			return nil, fmt.Errorf("a project on the server links a Mac project; pass only project_id and project_name")
		}
		links, err := setRemoteProjectLink(ctx, workspacePath, remoteProjectLink{ProjectID: project, Name: cleanAgentLabel(name)})
		if err != nil {
			return nil, err
		}
		log.Printf("[CREW_LINK] event=linked_mac project=%s mac_project=%s by=%s", crewProjectIDOfRoot(workspacePath), project, userID)
		return map[string]any{"linked_remote_projects": links}, nil
	}
	here, hereName, err := macProjectIdentity(ctx, userID, workspacePath)
	if err != nil {
		return nil, err
	}
	conns, err := desktopConnectionsOf(userID)
	if err != nil {
		return nil, fmt.Errorf("cannot read this Mac's server sign-ins: %v", err)
	}
	conn, err := pickServerConnection(conns, server)
	if err != nil {
		return nil, err
	}
	listed, err := callServerTool(ctx, userID, conn, "workspace", map[string]any{"action": "list"})
	if err != nil {
		return nil, err
	}
	serverName := ""
	items, _ := listed["workspaces"].([]any)
	for _, item := range items {
		if p, ok := item.(map[string]any); ok && stringField(p["id"]) == project {
			serverName = stringField(p["name"])
		}
	}
	if serverName == "" {
		return nil, fmt.Errorf("no project %s on %s that you can open; list them with server_project_files action=projects", project, conn.Name)
	}
	if _, err := callServerTool(ctx, userID, conn, "workspace", map[string]any{"action": "reference_attach", "crew_id": project, "project_id": here, "project_name": hereName}); err != nil {
		return nil, err
	}
	links, err := setRemoteProjectLink(ctx, workspacePath, remoteProjectLink{ProjectID: project, Name: cleanAgentLabel(serverName), Server: conn.ID})
	if err != nil {
		return nil, err
	}
	log.Printf("[CREW_LINK] event=linked_server project=%s server=%s server_project=%s", here, conn.Name, project)
	return map[string]any{"linked_remote_projects": links, "note": fmt.Sprintf("Linked to %s on %s, both ways. Agents in either project can now list and message each other's agents (list_agents). Read its files with server_project_files.", serverName, conn.Name)}, nil
}

// unlinkRemoteProject removes the link on this side and, on the Mac, on the server too.
func unlinkRemoteProject(ctx context.Context, userID, workspacePath, project string) (string, error) {
	project = strings.TrimSpace(project)
	if !IsLocalMode() {
		if _, err := removeRemoteProjectLink(ctx, workspacePath, project); err != nil {
			return "", err
		}
		return "Link removed.", nil
	}
	here, _, err := macProjectIdentity(ctx, userID, workspacePath)
	if err != nil {
		return "", err
	}
	removed, err := removeRemoteProjectLink(ctx, workspacePath, project)
	if err != nil {
		return "", err
	}
	for _, link := range removed {
		conn, err := desktopConnectionFor(userID, link.Server)
		if err != nil {
			return "Link removed on this Mac. This Mac is no longer signed in to that server; remove the link there from that project's chat if it still shows.", nil
		}
		if _, err := callServerTool(ctx, userID, conn, "workspace", map[string]any{"action": "reference_detach", "crew_id": project, "project_id": here}); err != nil {
			return fmt.Sprintf("Link removed on this Mac. %s did not remove its side (%v); remove it there from that project's chat.", conn.Name, err), nil
		}
	}
	return "Link removed on this Mac and on the server.", nil
}

// macProjectIdentity is this Mac project's ID and name. Only the project's owner may link it: a server sign-in is the
// owner's, never a teammate's.
func macProjectIdentity(ctx context.Context, userID, workspacePath string) (string, string, error) {
	roots, found, err := crewStorageRoots(workspacePath)
	if err != nil || !found || roots.Owner != userID {
		return "", "", fmt.Errorf("only this project's owner can link it to a project on a server")
	}
	here := crewProjectIDOfRoot(workspacePath)
	if here == "" {
		return "", "", fmt.Errorf("this is not a project")
	}
	name := here
	if manifest, err := readCrewProjectManifests(ctx, crewProfileID, workspacePath); err == nil && manifest.displayTitle() != "" {
		name = manifest.displayTitle()
	}
	return here, name, nil
}

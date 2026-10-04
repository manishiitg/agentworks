package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"log"
	"strings"
)

// Context paths are untrusted request input. Resolve each existing workflow
// against this request's live user permissions before reading its context or
// granting tool/shell access. An unreadable manifest must never grant access.
func authorizeWorkflowContextPaths(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	denied := errors.New("One or more attached workflows are unavailable or you no longer have access. Remove the attachment and select an accessible workflow.")
	claims := GetUserFromContext(ctx)
	seen := map[string]bool{}
	result := make([]string, 0, len(paths))
	for _, raw := range paths {
		folder := strings.TrimSuffix(strings.TrimSpace(raw), "/")
		parts := strings.Split(folder, "/")
		if len(parts) != 2 || parts[0] != "Workflow" || parts[1] == "" || parts[1] == "." || parts[1] == ".." || strings.ContainsAny(folder, "\\\x00") {
			return nil, denied
		}
		if seen[folder] {
			continue
		}
		manifest, exists, err := ReadWorkflowManifest(ctx, folder)
		if err != nil || !exists || manifest == nil || !userAllowedWorkflowID(claims, manifest.ID) || workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
			return nil, denied
		}
		seen[folder] = true
		result = append(result, folder)
	}
	return result, nil
}

func contextReferenceReadRoot(userID, folder string) (string, bool) {
	folder = strings.TrimSuffix(strings.TrimSpace(folder), "/")
	if strings.ContainsAny(folder, "\\\x00") {
		return "", false
	}
	parts := strings.Split(folder, "/")
	switch {
	case len(parts) == 2 && parts[0] == "Workflow" && parts[1] != "" && parts[1] != "." && parts[1] != "..":
		return folder, true
	case isCrewContextShape(parts) && strings.TrimSpace(userID) != "":
		// A Crew is addressed by Crew/<project> (shared root), by its physical _users/<owner>/Chats/Work/projects/
		// <project> path (anyone's), or by the caller's own Chats/Work/projects/<project>. The folder-guard root is
		// where the Crew lives now: a reference stored before the Crew moved keeps working through the alias
		// resolver (PLAT-442 step 4).
		ref, ok := resolveCrewPath(context.Background(), userID, folder)
		if !ok || ref.Rest != "" {
			return "", false
		}
		return ref.Root, true
	default:
		return "", false
	}
}

// isCrewContextShape reports whether parts spell a Crew project root in one of the three accepted spellings
// (and nothing below it): Crew/<project>, Chats/Work/projects/<project>, _users/<owner>/Chats/Work/projects/<project>.
func isCrewContextShape(parts []string) bool {
	valid := func(name string) bool {
		return name != "" && name != "." && name != ".." && !strings.HasPrefix(name, ".")
	}
	switch {
	case len(parts) == 2 && parts[0] == crewSharedRootName:
		return valid(parts[1])
	case len(parts) == 4 && parts[0] == "Chats" && parts[1] == "Work" && parts[2] == "projects":
		return valid(parts[3])
	}
	return isOtherOwnerCrewPath(parts)
}

// authorizeWorkflowContextPathsWithReadRoots keeps the durable/user-visible
// reference canonical while separately resolving the folder-guard root. Crew
// projects live below _users/<id>/..., but their public project identity is the
// stable Chats/Work/projects/<project> path used by the picker and workflow.json.
func authorizeWorkflowContextPathsWithReadRoots(ctx context.Context, paths []string) ([]string, []string, error) {
	return authorizeContextPathsWithReadRoots(ctx, paths, false)
}

// authorizeContextPathsWithReadRoots is authorizeWorkflowContextPathsWithReadRoots
// with one relaxation for a turn's saved attachments (skipMissing): an attached
// workflow or crew that no longer exists (its workflow.json / product.json is
// definitely absent) is dropped with a log line instead of failing the whole
// turn -- deleting a crew must not break every crew that had attached it.
// Anything else (a read error, a malformed manifest, no access) still denies.
func authorizeContextPathsWithReadRoots(ctx context.Context, paths []string, skipMissing bool) ([]string, []string, error) {
	if len(paths) == 0 {
		return nil, nil, nil
	}
	denied := errors.New("One or more attached workflows or Crew projects are unavailable or you no longer have access. Remove the attachment and select an accessible project.")
	claims := GetUserFromContext(ctx)
	seen := map[string]bool{}
	result := make([]string, 0, len(paths))
	readRoots := make([]string, 0, len(paths))
	for _, raw := range paths {
		folder := strings.TrimSuffix(strings.TrimSpace(raw), "/")
		parts := strings.Split(folder, "/")
		if strings.ContainsAny(folder, "\\\x00") {
			logContextDenial(claims, folder, "invalid characters")
			return nil, nil, denied
		}
		if seen[folder] {
			continue
		}
		userID := ""
		if claims != nil {
			userID = claims.UserID
		}
		readRoot, validShape := contextReferenceReadRoot(userID, folder)
		if !validShape {
			logContextDenial(claims, folder, "not a workflow or crew path")
			return nil, nil, denied
		}
		switch {
		case len(parts) == 2 && parts[0] == "Workflow" && parts[1] != "" && parts[1] != "." && parts[1] != "..":
			if skipMissing {
				if _, exists, err := ReadWorkflowManifest(ctx, folder); err == nil && !exists {
					log.Printf("[WORKFLOW_CONTEXT] Skipping attached workflow %s: it no longer exists", folder)
					continue
				}
			}
			if _, err := authorizeWorkflowContextPaths(ctx, []string{folder}); err != nil {
				logContextDenial(claims, folder, "workflow not readable by this user")
				return nil, nil, denied
			}
		case isCrewContextShape(parts):
			if claims == nil || strings.TrimSpace(claims.UserID) == "" {
				logContextDenial(claims, folder, "no user for a crew attachment")
				return nil, nil, denied
			}
			rawManifest, exists, err := readFileFromWorkspace(ctx, readRoot+"/product.json")
			if skipMissing && err == nil && !exists {
				log.Printf("[WORKFLOW_CONTEXT] Skipping attached crew %s: it no longer exists", folder)
				continue
			}
			if err != nil || !exists {
				logContextDenial(claims, folder, "crew has no product.json")
				return nil, nil, denied
			}
			var manifest productProjectManifest
			if json.Unmarshal([]byte(rawManifest), &manifest) != nil || !strings.EqualFold(strings.TrimSpace(manifest.Product), "work") || strings.TrimSpace(manifest.ID) == "" {
				logContextDenial(claims, folder, "crew product.json is not a Work crew")
				return nil, nil, denied
			}
		default:
			logContextDenial(claims, folder, "unsupported attachment path")
			return nil, nil, denied
		}
		seen[folder] = true
		result = append(result, folder)
		readRoots = append(readRoots, readRoot)
	}
	return result, readRoots, nil
}

// mergeDurableWorkflowContextPaths adds workflow.json links to the transient
// # references supplied by a client. Authorization intentionally remains in
// authorizeWorkflowContextPaths so saved links are rechecked on every turn.
func mergeDurableWorkflowContextPaths(ctx context.Context, selectedFolder string, transient []string) []string {
	selected := strings.TrimSuffix(strings.TrimSpace(selectedFolder), "/")
	if !strings.HasPrefix(selected, "Workflow/") {
		return appendUniqueStrings(nil, transient...)
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, selected)
	if err != nil || !exists || manifest == nil {
		return appendUniqueStrings(nil, transient...)
	}
	return appendUniqueStrings(manifest.WorkflowContextPaths, transient...)
}

// isOtherOwnerCrewPath reports whether parts spell a physical Crew project
// path _users/<owner>/Chats/Work/projects/<project>.
func isOtherOwnerCrewPath(parts []string) bool {
	joined := strings.Join(parts, "/")
	ref := workspaceref.MustParse(joined)
	root, _, ok := ref.ProjectRoot()
	return ok && root == workspaceref.CrewProjectsRoot && ref.HasOwner() && ref.String() == joined
}

// admitTurnContextPaths merges a turn's saved workflow links with its
// one-message # references and authorizes them, recording the allowed paths
// and their read roots on req. handleQuery and the bot dry run share it.
func admitTurnContextPaths(ctx context.Context, req *QueryRequest) error {
	req.WorkflowContextPaths = mergeDurableWorkflowContextPaths(ctx, req.SelectedFolder, req.WorkflowContextPaths)
	contextPaths, contextReadPaths, err := authorizeContextPathsWithReadRoots(workflowContextAuthorizationContext(ctx), req.WorkflowContextPaths, true)
	if err != nil {
		return err
	}
	req.WorkflowContextPaths = contextPaths
	req.authorizedWorkflowContextReadPaths = contextReadPaths
	return nil
}

// workflowContextAuthorizationContext is the identity a turn's attachments
// are checked against. A bot turn runs on its resource owner's behalf (the
// crew or trigger owner, execution_principal.go), and the attachments saved on
// that crew are the owner's context: they are checked as the owner, so a crew
// answering in Slack has the same attached context as in the owner's web chat
// (user decision 2026-09-26). Every other turn is checked as its caller.
func workflowContextAuthorizationContext(ctx context.Context) context.Context {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.ExecutionPrincipal == nil {
		return ctx
	}
	ownerID := strings.TrimSpace(claims.ExecutionPrincipal.ResourceOwnerID)
	if ownerID == "" {
		return ctx
	}
	owner := &UserClaims{UserID: ownerID, Username: ownerID}
	if record := directoryUserFor(ownerID, "", ""); record != nil {
		owner.Username = record.Username
		owner.Email = record.Email
	}
	return context.WithValue(ctx, UserContextKey, owner)
}

// logContextDenial records which attachment refused a turn and why; the
// user-facing error is deliberately generic.
func logContextDenial(claims *UserClaims, folder, reason string) {
	userID, principal := "", ""
	if claims != nil {
		userID = claims.UserID
		if claims.ExecutionPrincipal != nil {
			principal = claims.ExecutionPrincipal.Kind
		}
	}
	log.Printf("[CONTEXT_ACCESS] attachment %q refused for user=%s principal=%s: %s", folder, userID, principal, reason)
}

package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

// brainFolderName is Brain's folder at the top of the documents tree: its notes as plain files, a Git working folder
// the Brain chat edits and backs up with git like any product's folder (PLAT-633). Only administrators reach it
// through the Files proxy; everyone else uses Brain's tools and folder roles.
const brainFolderName = "Brain"

// moveBrainNotesIntoDocuments copies Brain's notes from their old place in the state folder to Brain/ once, then
// renames the old folder aside (kept, not deleted). Nothing happens when Brain/ already holds anything.
func moveBrainNotesIntoDocuments(oldLive, newLive string) error {
	if oldLive == newLive {
		return nil
	}
	if entries, err := os.ReadDir(newLive); err == nil && len(entries) > 0 {
		return nil
	}
	entries, err := os.ReadDir(oldLive)
	if os.IsNotExist(err) || err == nil && len(entries) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	staging := filepath.Join(filepath.Dir(newLive), "."+filepath.Base(newLive)+"-moving")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := copyBrainTree(oldLive, staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := os.Remove(newLive); err != nil && !os.IsNotExist(err) {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := os.Rename(staging, newLive); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	return os.Rename(oldLive, oldLive+".moved-"+time.Now().UTC().Format("20060102T150405Z"))
}

func copyBrainTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("unexpected symbolic link %s", rel)
		case info.IsDir():
			return os.MkdirAll(target, 0o700)
		case !info.Mode().IsRegular():
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Sync(); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// brainShellGrant decides whether this Brain chat turn gets Brain's raw folder: only a person who is Owner of the
// whole Brain (administrators are), since a shell there bypasses every folder role. It prepares the folder as a Git
// repository and returns the shell environment git needs: the backup token (from its platform secret) for the
// folder's credential helper, and the person as the commit author.
func brainShellGrant(r *http.Request) (map[string]string, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		return nil, false
	}
	service, err := knowledgebaseService()
	if err != nil {
		return nil, false
	}
	if err := knowledgebaseSyncIdentities(r.Context(), service); err != nil {
		return nil, false
	}
	if !service.CanEditWholeBrain(knowledgebasePrincipal(r, claims)) {
		return nil, false
	}
	env := map[string]string{}
	folder, err := service.EnsureGitRepository(r.Context())
	if err != nil {
		log.Printf("[BRAIN] Git folder not ready: %v", err)
	}
	if token := service.GitToken(folder); token != "" {
		env[knowledgebase.GitTokenEnv] = token
	}
	if folder.Username != "" {
		env["BRAIN_GIT_USERNAME"] = folder.Username
	}
	name := strings.TrimSpace(claims.Username)
	if name == "" {
		name = claims.UserID
	}
	email := strings.TrimSpace(claims.Email)
	if email == "" {
		email = claims.UserID + "@brain.local"
	}
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		env[key] = name
	}
	for _, key := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		env[key] = email
	}
	return env, true
}

// syncBrainAfterFileTools wraps the shell and file-edit tools of a Brain chat that has Brain's folder: after each
// call Brain picks up what changed on disk, recorded as made by this person.
func syncBrainAfterFileTools(executors map[string]func(context.Context, map[string]interface{}) (string, error), userID string) {
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file"} {
		run, ok := executors[name]
		if !ok {
			continue
		}
		executors[name] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			out, err := run(ctx, args)
			if service, serr := knowledgebaseService(); serr == nil {
				if syncErr := service.SyncDisk(context.WithoutCancel(ctx), userID); syncErr != nil {
					out += "\n\nBrain could not take some changed files: " + syncErr.Error()
				}
			}
			return out, err
		}
	}
}

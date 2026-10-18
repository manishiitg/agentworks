package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type workflowSkillsEditKey struct{}
type teamCommitReadKey struct{}

// The typed migration uses the existing staged edit and anchored commit for
// both text and binary assets. Nothing is copied before full authorization.
func readWorkflowSkillsFile(root *os.Root, relative string) (string, bool, error) {
	if err := externalNoSymlinks(root, relative); err != nil {
		return "", false, err
	}
	f, err := root.Open(relative)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("migration source must be an ordinary file")
	}
	const nativeLimit = 8 << 20 // Same preparation limit as stageTeamWrite.
	if info.Size() > nativeLimit {
		return "", false, fmt.Errorf("migration file exceeds the native edit size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(f, nativeLimit+1))
	if err != nil {
		return "", false, err
	}
	if len(raw) > nativeLimit {
		return "", false, fmt.Errorf("migration file exceeds the native edit size limit")
	}
	return string(raw), true, nil
}

func workflowSkillsRelative(root, p string, write bool) (string, error) {
	if !strings.HasPrefix(p, root+"/") {
		return "", fmt.Errorf("migration cannot access another workflow")
	}
	rel := strings.TrimPrefix(p, root+"/")
	clean, err := wf.CleanRelative(rel)
	if err != nil || clean != rel || wf.Private(clean) {
		return "", fmt.Errorf("invalid migration path")
	}
	allowed := clean == "workflow.json" || strings.HasPrefix(clean, "planning/") || strings.HasPrefix(clean, "skills/") || !write && strings.HasPrefix(clean, "learnings/")
	if !allowed {
		return "", fmt.Errorf("migration only changes this workflow's guides, skill selections and audit")
	}
	return clean, nil
}

func executeWorkflowSkillsBuilderTool(ctx context.Context, rootPath, user string, args map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) (string, error) {
	ctx = teamUserContext(ctx, user)
	manifest, found, err := ReadWorkflowManifest(ctx, rootPath)
	claims := &UserClaims{UserID: user}
	if err != nil {
		return "", err
	}
	if !found || !externalFileAuthoringAllowed(claims, manifest) {
		return "", fmt.Errorf("ask this workflow's Builder to migrate its guides")
	}
	if manifest.Team != nil && manifest.Team.LeaderID != user {
		return "", fmt.Errorf("shared workflow settings require the Leader")
	}
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		return "", err
	}
	defer base.Close()
	root, err := wf.OpenDirectory(base, rootPath, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	ctx = prepareTeamEdit(ctx, rootPath, stringToolArg(args, "expected_plan_revision"))
	ctx = context.WithValue(ctx, workflowSkillsEditKey{}, true)
	ctx = context.WithValue(ctx, teamManifestPermitKey{}, true)
	read := func(c context.Context, p string) (string, bool, error) {
		if raw, exists, handled := stagedTeamRead(c, p); handled {
			return raw, exists, nil
		}
		rel, err := workflowSkillsRelative(rootPath, p, false)
		if err != nil {
			return "", false, err
		}
		raw, exists, err := readWorkflowSkillsFile(root, rel)
		if err == nil {
			rememberTeamPlanRead(c, p, raw)
		}
		return raw, exists, err
	}
	nativeRead := func(c context.Context, p string) (string, error) {
		raw, found, err := read(c, p)
		if err == nil && !found {
			err = os.ErrNotExist
		}
		return raw, err
	}
	stage := func(c context.Context, p, raw string) error {
		if _, err := workflowSkillsRelative(rootPath, p, true); err != nil {
			return err
		}
		_, err := stageTeamWrite(c, p, raw)
		return err
	}
	ctx = stepworkflow.WithWorkflowSkillsFiles(ctx, nativeRead, stage)
	result, err := execute(ctx, map[string]interface{}{})
	if err != nil {
		return "", err
	}
	write := func(c context.Context, p, raw string) error {
		rel, err := workflowSkillsRelative(rootPath, p, true)
		if err != nil {
			return err
		}
		return wf.ReplaceNativeManagedFile(root, rel, []byte(raw))
	}
	remove := func(c context.Context, p string) error {
		rel, err := workflowSkillsRelative(rootPath, p, true)
		if err != nil {
			return err
		}
		parent, err := wf.OpenDirectory(root, path.Dir(rel), false)
		if err != nil {
			return err
		}
		defer parent.Close()
		return parent.Remove(path.Base(rel))
	}
	if err = commitTeamEditWithFiles(ctx, result, read, write, remove); err != nil {
		return "", err
	}
	return result, nil
}

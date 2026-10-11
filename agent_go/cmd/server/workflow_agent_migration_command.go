package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/spf13/cobra"
)

var migrateWorkflowAgentStepsCmd = &cobra.Command{
	Use:   "migrate-workflow-agent-steps workspace-docs-root [workspace-docs-root...]",
	Short: "Prepare v1.0.47 agent steps and stamp each checked workflow during a drained deploy",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, roots []string) error {
		apply, _ := cmd.Flags().GetBool("apply")
		drained, _ := cmd.Flags().GetBool("server-drained")
		_, err := migrateWorkflowAgentStepsFromDisk(roots, apply, drained, cmd.OutOrStdout())
		return err
	},
}

func init() {
	migrateWorkflowAgentStepsCmd.Flags().Bool("apply", false, "apply the reviewed batch; default is dry-run")
	migrateWorkflowAgentStepsCmd.Flags().Bool("server-drained", false, "confirm all runs and writers are stopped")
	ServerCmd.AddCommand(migrateWorkflowAgentStepsCmd)
}

type agentMigrationFile struct {
	root          *os.Root
	name          string
	before, after []byte
	mode          fs.FileMode
}

type agentMigrationCandidate struct {
	plan, manifest agentMigrationFile
	steps          int
}

// This command has no startup hook. It changes only live native workflow plans
// and their final contract rung, after every candidate passes preflight.
func migrateWorkflowAgentStepsFromDisk(roots []string, apply, drained bool, out io.Writer) (int, error) {
	if apply && !drained {
		return 0, fmt.Errorf("--apply requires --server-drained; stop all executions and document writers first")
	}
	var candidates []agentMigrationCandidate
	seen := map[string]bool{}
	now := time.Now().UTC()
	for _, rootPath := range roots {
		absolute, err := filepath.Abs(rootPath)
		if err != nil {
			return 0, err
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return 0, err
		}
		if resolved != absolute {
			return 0, fmt.Errorf("refusing symlink root %s", rootPath)
		}
		root, err := os.OpenRoot(absolute)
		if err != nil {
			return 0, err
		}
		defer root.Close()
		err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "runs", "releases", "versions", "backup", "backups", "moved-workflows", ".trash", ".calls", ".relay_releases", ".git", "node_modules":
					return fs.SkipDir
				}
				return nil
			}
			if entry.Name() != "plan.json" || path.Base(path.Dir(name)) != "planning" {
				return nil
			}
			identity := filepath.Join(absolute, filepath.FromSlash(name))
			if seen[identity] {
				return nil
			}
			seen[identity] = true
			manifestName := path.Join(path.Dir(path.Dir(name)), "workflow.json")
			plan, err := agentMigrationReadFile(root, name)
			if err != nil {
				return fmt.Errorf("%s: %w", identity, err)
			}
			manifest, err := agentMigrationReadFile(root, manifestName)
			if err != nil {
				return fmt.Errorf("%s: %w", manifestName, err)
			}
			var kind struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(manifest.before, &kind); err != nil {
				return err
			}
			if kind.Kind == "relay" {
				_, err := fmt.Fprintf(out, "skip Relay: %s (use its own contract migration)\n", identity)
				return err
			}
			planAfter, manifestAfter, count, err := stepworkflow.MigrateAgentStepDeployment(string(plan.before), string(manifest.before), now)
			if err != nil {
				return fmt.Errorf("%s: %w", identity, err)
			}
			plan.after, manifest.after = []byte(planAfter), []byte(manifestAfter)
			if !bytes.Equal(plan.before, plan.after) || !bytes.Equal(manifest.before, manifest.after) {
				candidates = append(candidates, agentMigrationCandidate{plan: plan, manifest: manifest, steps: count})
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	// Check every source again before the first write. A drain is still required;
	// no multi-file filesystem transaction can prevent an unrelated writer.
	for _, candidate := range candidates {
		for _, file := range []agentMigrationFile{candidate.plan, candidate.manifest} {
			current, err := agentMigrationReadFile(file.root, file.name)
			if err != nil {
				return 0, err
			}
			if !bytes.Equal(current.before, file.before) {
				return 0, fmt.Errorf("%s changed during preflight; drain every writer", file.name)
			}
		}
	}
	for _, candidate := range candidates {
		mode := "dry-run"
		if apply {
			mode = "apply"
		}
		if _, err := fmt.Fprintf(out, "%s: %s: %d agent records, checked contract %s\n", mode, candidate.plan.name, candidate.steps, stepworkflow.AgentStepContractVersion); err != nil {
			return 0, err
		}
		if !apply {
			continue
		}
		// The plan is written first; the contract marker is always last. A partial
		// I/O failure keeps the workflow gated and can be resumed while drained.
		if err := agentMigrationReplace(candidate.plan); err != nil {
			return 0, err
		}
		current, err := candidate.plan.root.ReadFile(candidate.plan.name)
		if err != nil {
			return 0, err
		}
		if err := stepworkflow.ValidateAgentStepContractStamp(string(current)); err != nil {
			return 0, err
		}
		if err := agentMigrationWriteReceipt(candidate, now); err != nil {
			return 0, err
		}
		if err := agentMigrationReplace(candidate.manifest); err != nil {
			return 0, err
		}
	}
	_, err := fmt.Fprintf(out, "%d workflow(s) %s\n", len(candidates), map[bool]string{false: "would change", true: "updated"}[apply])
	return len(candidates), err
}

func agentMigrationReadFile(root *os.Root, name string) (agentMigrationFile, error) {
	if err := externalNoSymlinks(root, name); err != nil {
		return agentMigrationFile{}, err
	}
	info, err := root.Stat(name)
	if err != nil {
		return agentMigrationFile{}, err
	}
	if !info.Mode().IsRegular() {
		return agentMigrationFile{}, fmt.Errorf("%s must be a regular file", name)
	}
	raw, err := root.ReadFile(name)
	return agentMigrationFile{root: root, name: name, before: raw, mode: info.Mode().Perm()}, err
}

func agentMigrationReplace(file agentMigrationFile) error {
	if bytes.Equal(file.before, file.after) {
		return nil
	}
	current, err := agentMigrationReadFile(file.root, file.name)
	if err != nil {
		return err
	}
	if !bytes.Equal(current.before, file.before) {
		return fmt.Errorf("%s changed during apply; stop and reconcile", file.name)
	}
	backup := file.name + ".pre-agent-v1.0.47.bak"
	if err := agentMigrationCreate(file.root, backup, file.before, file.mode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	temporary := path.Join(path.Dir(file.name), ".agent-migration-"+uuid.NewString())
	if err := agentMigrationCreate(file.root, temporary, file.after, file.mode); err != nil {
		return err
	}
	defer file.root.Remove(temporary)
	return file.root.Rename(temporary, file.name)
}

func agentMigrationCreate(root *os.Root, name string, content []byte, mode fs.FileMode) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	if writeErr == nil {
		writeErr = file.Chmod(mode)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = root.Remove(name)
		return writeErr
	}
	return closeErr
}

func agentMigrationWriteReceipt(candidate agentMigrationCandidate, now time.Time) error {
	directory := path.Join(path.Dir(candidate.plan.name), "changelog")
	if err := candidate.plan.root.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := externalNoSymlinks(candidate.plan.root, directory); err != nil {
		return err
	}
	entry := stepworkflow.PlanChangelogEntry{
		ChangeID: uuid.NewString(), Timestamp: now.Format(time.RFC3339), Tool: "migrate_agent_steps",
		Reason: "PLAT-861/PLAT-851: drained deployment batch for contract v1.0.47.", Target: "planning/plan.json",
		BeforeRef: fmt.Sprintf("sha256:%x", sha256.Sum256(candidate.plan.before)), AfterRef: fmt.Sprintf("sha256:%x", sha256.Sum256(candidate.plan.after)),
		NoOp: bytes.Equal(candidate.plan.before, candidate.plan.after), Actor: "system", DependencyClass: "plan",
		Origin: stepworkflow.PlanChangeOrigin{Type: "system", AgentName: "drained deployment migration"},
	}
	raw, err := json.MarshalIndent(stepworkflow.PlanChangelog{Entries: []stepworkflow.PlanChangelogEntry{entry}}, "", "  ")
	if err != nil {
		return err
	}
	return agentMigrationCreate(candidate.plan.root, path.Join(directory, "changelog-agent-migration-"+entry.ChangeID+".json"), append(raw, '\n'), 0600)
}

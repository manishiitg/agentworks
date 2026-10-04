package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

var migrateCrewsToSharedRootCmd = &cobra.Command{
	Use:   "migrate-crews-to-shared-root",
	Short: "Move Crews from their owners' private trees to the shared Crew/ root (PLAT-442 step 4)",
	Long: `Moves each Crew from _users/<owner>/Chats/Work/projects/<folder> to Crew/<folder>, keeping the folder name.

Without --apply this is a dry run: it prints every Crew with its owner, source, destination, size, collisions, readers and the stored references that will keep resolving through the alias, and what blocks it. It changes nothing.

--apply needs --backup-dir. It first copies exactly the Crew folders it is about to move (and the owner registry) into the backup directory and reads the copy back to verify it, then moves ONE Crew at a time: copy into Crew/.migrating/<folder>, compare every file's hash, check the source did not change, then switch two renames in. The old folder is kept beside its owner's projects until --finalize. Every step is journaled in <state root>/migrations/crew-move/, so a crash is resumed by running the command again. Run it while the Crews being moved are idle and the server is quiet; a Crew with a live tmux session or process, an [OWNER_MISMATCH], a collision at the shared root, a symlink that leaves the folder, or too little disk space is skipped and reported.

--rollback <folder> puts a Crew back (its current folder, with anything written since the move; --from-backup restores the backed-up copy instead). --finalize removes the kept old folders of finished moves. --crew limits a run to the named folders or project ids (repeatable): move one Crew first.

New Crews are created at Crew/<folder> only when the server runs with AGENTWORKS_CREW_SHARED_ROOT=on; turn it on after the migration has been applied and verified.`,
	RunE: runMigrateCrewsToSharedRoot,
}

func init() {
	cmd := migrateCrewsToSharedRootCmd
	cmd.Flags().String("docs-root", "", "workspace documents root (defaults to WORKSPACE_DOCS_PATH)")
	cmd.Flags().String("state-root", "", "AgentWorks state root (defaults to AGENTWORKS_STATE_ROOT)")
	cmd.Flags().Bool("dry-run", true, "report only; this is the default")
	cmd.Flags().Bool("apply", false, "move the Crews (needs --backup-dir)")
	cmd.Flags().String("backup-dir", "", "directory that receives the verified backup of the Crews about to move (required with --apply)")
	cmd.Flags().StringSlice("crew", nil, "only this Crew (folder name or project id); repeatable")
	cmd.Flags().String("rollback", "", "put this Crew (folder name) back in its owner's tree")
	cmd.Flags().Bool("from-backup", false, "with --rollback: restore the Crew from the backup instead of moving its current folder back")
	cmd.Flags().Bool("finalize", false, "remove the kept old folders of finished moves (all, or the --crew ones)")
	cmd.Flags().Bool("no-reference-scan", false, "skip counting stored references (faster on a large server)")
	cmd.Flags().Bool("json", false, "print the report as JSON")
}

func runMigrateCrewsToSharedRoot(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()
	docsRoot, _ := flags.GetString("docs-root")
	if strings.TrimSpace(docsRoot) == "" {
		docsRoot = fsutil.WorkspaceDocsRoot()
	}
	stateRoot, _ := flags.GetString("state-root")
	if strings.TrimSpace(stateRoot) == "" {
		var err error
		if stateRoot, err = workflowCLIStateRoot(); err != nil {
			return err
		}
	}
	apply, _ := flags.GetBool("apply")
	dryRun, _ := flags.GetBool("dry-run")
	if apply && flags.Changed("dry-run") && dryRun {
		return fmt.Errorf("--apply and --dry-run contradict each other")
	}
	backup, _ := flags.GetString("backup-dir")
	crews, _ := flags.GetStringSlice("crew")
	rollback, _ := flags.GetString("rollback")
	fromBackup, _ := flags.GetBool("from-backup")
	finalize, _ := flags.GetBool("finalize")
	noRefs, _ := flags.GetBool("no-reference-scan")
	asJSON, _ := flags.GetBool("json")
	if (finalize && apply) || (rollback != "" && (apply || finalize)) {
		return fmt.Errorf("--apply, --rollback and --finalize are separate runs")
	}
	if fromBackup && rollback == "" {
		return fmt.Errorf("--from-backup is only for --rollback")
	}
	report, err := runCrewMove(context.Background(), crewMoveOptions{
		DocsRoot: docsRoot, StateRoot: stateRoot, BackupDir: backup, Apply: apply, Crews: crews,
		Rollback: rollback, FromBackup: fromBackup, Finalize: finalize, SkipReferenceScan: noRefs, Out: cmd.ErrOrStderr(),
	})
	if report != nil {
		if asJSON {
			encoded, _ := json.MarshalIndent(report, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
		} else {
			printCrewMoveReport(cmd.OutOrStdout(), report)
		}
	}
	if errors.Is(err, errCrewMoveIncomplete) {
		// The report says what; the exit status says it was not all done.
		return err
	}
	return err
}

// printCrewMoveReport is the human-readable dry run and result.
func printCrewMoveReport(out io.Writer, r *crewMoveReport) {
	fmt.Fprintf(out, "Crew move (%s)\n  docs root:  %s\n  state root: %s\n", r.Mode, r.DocsRoot, r.StateRoot)
	if r.BackupRun != "" {
		fmt.Fprintf(out, "  backup:     %s (verified readable before any move)\n", r.BackupRun)
	}
	ready, blocked := 0, 0
	for _, plan := range r.Crews {
		if len(plan.Blockers) > 0 {
			blocked++
		} else {
			ready++
		}
	}
	fmt.Fprintf(out, "  %d Crew(s) found: %d can move, %d blocked\n", len(r.Crews), ready, blocked)
	for _, plan := range r.Crews {
		fmt.Fprintf(out, "\n%s  (owner %s)  state: %s\n", plan.Folder, plan.Owner, plan.State)
		if plan.Title != "" || plan.ProjectID != "" {
			fmt.Fprintf(out, "  title:        %s   project id: %s\n", plan.Title, plan.ProjectID)
		}
		fmt.Fprintf(out, "  move:         %s  ->  %s\n", plan.Source, plan.Dest)
		fmt.Fprintf(out, "  size:         %s in %d files, %d folders, %d symlinks\n", humanBytes(plan.Bytes), plan.Files, plan.Dirs, plan.Symlinks)
		owner := plan.RegistryOwner
		if owner == "" {
			owner = "(none yet; the path's owner is used and registered)"
		}
		fmt.Fprintf(out, "  owner:        path says %s, registry: %s [%s], manifest owner_id: %s\n", plan.Owner, owner, plan.RegistryState, orDash(plan.ManifestOwner))
		fmt.Fprintf(out, "  access after: owner %s full; every other user with the Crew product reads (Run mode, only while project sharing is on); users without it get nothing\n", plan.Owner)
		if len(plan.Readers) > 0 {
			fmt.Fprintf(out, "  readers:      users with the Crew product: %s\n", strings.Join(plan.Readers, ", "))
		}
		if plan.RuntimeFolders > 0 {
			fmt.Fprintf(out, "  CLI runtimes: %d runtime folder(s) link to this Crew; they keep their folder (and native sessions) and their link is repointed\n", plan.RuntimeFolders)
		}
		if len(plan.References) == 0 {
			fmt.Fprintf(out, "  references:   none found in workflows, configuration, chat history or other Crews\n")
		}
		for _, hit := range plan.References {
			fmt.Fprintf(out, "  references:   %d file(s) of %s mention it; kept as they are and served through the alias (nothing is rewritten)\n", hit.Files, hit.Kind)
		}
		for _, w := range plan.Warnings {
			fmt.Fprintf(out, "  note:         %s\n", w)
		}
		for _, b := range plan.Blockers {
			fmt.Fprintf(out, "  BLOCKED:      %s\n", b)
		}
	}
	for _, p := range r.Problems {
		fmt.Fprintf(out, "\nproblem: %s\n", p)
	}
	if len(r.Moved) > 0 {
		fmt.Fprintf(out, "\nmoved: %s\n", strings.Join(r.Moved, ", "))
	}
	if len(r.Resumed) > 0 {
		fmt.Fprintf(out, "resumed and finished: %s\n", strings.Join(r.Resumed, ", "))
	}
	if len(r.Verified) > 0 {
		fmt.Fprintf(out, "verified (hashes, owner, alias, access): %s\n", strings.Join(r.Verified, ", "))
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(out, "skipped (blocked): %s\n", strings.Join(r.Skipped, ", "))
	}
	for folder, why := range r.Failed {
		fmt.Fprintf(out, "FAILED %s: %s\n", folder, why)
	}
	if r.Mode == "dry-run" {
		fmt.Fprintf(out, "\nThis was a dry run. Nothing was changed. To move, run again with --apply --backup-dir <dir> (start with one Crew: --crew <folder>).\n")
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

var _ = os.Stderr

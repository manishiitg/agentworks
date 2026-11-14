package server

// PLAT-746-1020: one-off offline planner, deleted after every server and Mac
// has moved. The runtime never reads the retired Whole schema.
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/spf13/cobra"
)

type crewTeamFolderSelectionPlan struct {
	Owner   string   `json:"owner"`
	Root    string   `json:"root"`
	Folders []string `json:"folders"`
	Omitted int      `json:"omitted"`
}

func init() {
	ServerCmd.AddCommand(&cobra.Command{
		Use: "team-folder-selection-plan", Short: "Preview the one-off Team folder conversion without starting services or writing state", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return planCrewTeamFolderSelections(cmd.OutOrStdout()) },
	})
}

func planCrewTeamFolderSelections(out io.Writer) error {
	state, err := crewroot.StateRoot()
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(state, "team-folders", "*", "team.json"))
	if err != nil {
		return err
	}
	plan := map[string]crewTeamFolderSelectionPlan{}
	for _, file := range files {
		// This limited old-shape reader belongs only to this offline command.
		var saved struct {
			Project string `json:"project_id"`
			Owner   string `json:"owner_id"`
			Folder  struct {
				Whole bool `json:"whole"`
			} `json:"folder"`
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return err
		}
		if !saved.Folder.Whole {
			continue
		}
		if saved.Project != filepath.Base(filepath.Dir(file)) || saved.Owner == "" {
			return fmt.Errorf("invalid Team conversion identity")
		}
		row := crewTeamFolderSelectionPlan{Owner: saved.Owner, Root: "missing", Folders: []string{crewTeamDefaultFolder}}
		rec, admitted := crewOwnerRecord(saved.Project)
		if admitted && rec.OwnerID == saved.Owner {
			sync, err := crewroot.ReadSync(saved.Project)
			if err != nil {
				return err
			}
			if sync != nil && sync.Side == "server" && sync.Mode == "relay" {
				row.Root = "remote"
			} else {
				// Same owner registry and Crew resolver as the server. The removal variant
				// only avoids EnsurePrivateRoot/EnsureAutomation writes during this preview.
				roots, found, resolveErr := crewroot.LookupForRemoval(fsutil.WorkspaceDocsRoot(), crewProjectRootByID(saved.Project))
				if resolveErr != nil {
					return fmt.Errorf("project %s: Crew root resolution failed", saved.Project)
				}
				if found {
					entries, readErr := os.ReadDir(roots.Files)
					if readErr != nil && !os.IsNotExist(readErr) {
						return fmt.Errorf("project %s: Crew root listing failed", saved.Project)
					}
					if readErr == nil {
						row.Root = "found"
						row.Folders = []string{}
						for _, entry := range entries {
							if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
								continue
							}
							if clean, cleanErr := cleanCrewTeamFolders([]string{entry.Name()}, nil); cleanErr == nil && len(clean) == 1 && clean[0] == entry.Name() {
								row.Folders = append(row.Folders, entry.Name())
							}
						}
						slices.Sort(row.Folders)
						if len(row.Folders) > crewTeamMaxFolders {
							row.Omitted = len(row.Folders) - crewTeamMaxFolders
							row.Folders = row.Folders[:crewTeamMaxFolders]
						}
						if len(row.Folders) == 0 {
							row.Folders = []string{crewTeamDefaultFolder}
						}
					}
				}
			}
		}
		if _, err = cleanCrewTeamFolders(row.Folders, nil); err != nil {
			return fmt.Errorf("project %s: converted folder selection refused", saved.Project)
		}
		plan[saved.Project] = row
	}
	return json.NewEncoder(out).Encode(plan)
}

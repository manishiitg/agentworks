package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/cobra"
)

func executorCommand(o *options) *cobra.Command {
	var deviceID, stateDir string
	var folders, writeFolders, blocked, readOnly, shellFolders []string
	root := &cobra.Command{Use: "executor", Short: "Share explicitly selected local folders with server agents"}
	connect := &cobra.Command{Use: "connect", Args: cobra.NoArgs, Short: "Keep an outbound authenticated file and shell connection open; Ctrl-C disconnects", RunE: func(cmd *cobra.Command, _ []string) error {
		if !localfiles.ValidID(deviceID) {
			return errors.New("--device requires a stable identifier (letters, numbers, underscores or hyphens)")
		}
		if stateDir == "" {
			config, err := o.path()
			if err != nil {
				return err
			}
			stateDir = filepath.Join(filepath.Dir(config), "executor-state", deviceID)
		}
		config, err := o.path()
		if err != nil {
			return err
		}
		shells := map[string]bool{}
		for _, alias := range shellFolders {
			if !localfiles.ValidID(alias) {
				return errors.New("--shell requires a folder alias")
			}
			shells[alias] = true
		}
		var grants []localfiles.Grant
		for _, group := range []struct {
			values   []string
			writable bool
		}{{folders, false}, {writeFolders, true}} {
			for _, value := range group.values {
				alias, folder, ok := strings.Cut(value, "=")
				if !ok || !localfiles.ValidID(alias) || !filepath.IsAbs(folder) {
					return errors.New("folders must be ALIAS=/absolute/path")
				}
				guard := wf.FolderGuard{ReadPaths: []string{"."}, BlockedPaths: blocked, ReadOnlyPaths: readOnly}
				if group.writable {
					guard.WritePaths = []string{"."}
				}
				grants = append(grants, localfiles.Grant{Resource: localfiles.Resource{ID: alias, Writable: group.writable, Shell: shells[alias], Guard: guard}, Root: folder, State: filepath.Join(stateDir, alias), PrivatePaths: []string{config}})
			}
		}
		for alias := range shells {
			found := false
			for _, grant := range grants {
				if grant.ID == alias && grant.Writable {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("--shell %s requires a matching --write-folder", alias)
			}
		}
		if len(shells) > 0 {
			if err := configureLocalShellSandbox(); err != nil {
				return err
			}
		}
		executor, err := localfiles.Open(deviceID, grants)
		if err != nil {
			return err
		}
		defer executor.Close()
		client, err := o.client()
		if err != nil {
			return err
		}
		delay := time.Second
		for {
			err = client.ServeExecutor(cmd.Context(), executor, func() {
				delay = time.Second
				fmt.Fprintf(o.stderr, "Executor %s connected with %d folder(s). Ctrl-C disconnects.\n", deviceID, len(grants))
			})
			if cmd.Context().Err() != nil {
				return nil
			}
			var apiErr *agentworksclient.APIError
			if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 409) {
				return err
			}
			fmt.Fprintf(o.stderr, "Executor disconnected; reconnecting in %s.\n", delay)
			timer := time.NewTimer(delay)
			select {
			case <-cmd.Context().Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			delay = min(delay*2, 30*time.Second)
		}
	}}
	connect.Flags().StringVar(&deviceID, "device", "", "Stable device ID, e.g. work-laptop")
	connect.Flags().StringArrayVar(&folders, "folder", nil, "Read-only folder ALIAS=/absolute/path (repeatable)")
	connect.Flags().StringArrayVar(&writeFolders, "write-folder", nil, "Writable folder ALIAS=/absolute/path for guarded file edits (repeatable; raw plan writes remain blocked)")
	connect.Flags().StringArrayVar(&shellFolders, "shell", nil, "Allow sandboxed commands in a writable folder alias (repeatable; broader authority than guarded file edits)")
	connect.Flags().StringArrayVar(&blocked, "block", nil, "Blocked relative path on every shared folder (repeatable)")
	connect.Flags().StringArrayVar(&readOnly, "read-only", nil, "Read-only relative path on every writable folder (repeatable)")
	connect.Flags().StringVar(&stateDir, "state-dir", "", "Private receipt storage outside all shared folders")
	root.AddCommand(connect)
	return root
}

package main

import (
	"errors"
	"fmt"
	"os"
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
	var folders, writeFolders, blocked, readOnly []string
	var downloads bool
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
				grants = append(grants, localfiles.Grant{Resource: localfiles.Resource{ID: alias, Writable: group.writable, Shell: true, Guard: guard}, Root: folder, State: filepath.Join(stateDir, alias), PrivatePaths: []string{config}})
			}
		}
		if downloads {
			if len(grants) == 0 {
				return errors.New("--downloads requires a project --folder or --write-folder")
			}
			for i := range grants {
				if grants[i].ID == "downloads" {
					return errors.New("the downloads alias is reserved when --downloads is enabled")
				}
				grants[i].Downloads = true
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			grants = append(grants, localfiles.Grant{Resource: localfiles.Resource{ID: "downloads", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: blocked, ReadOnlyPaths: readOnly}}, Root: filepath.Join(home, "Downloads"), State: filepath.Join(stateDir, "downloads"), PrivatePaths: []string{config}})
		}
		if len(grants) > 0 {
			if err := configureLocalShellSandbox(); err != nil {
				return err
			}
		}
		executor, err := localfiles.Open(deviceID, grants)
		if err != nil {
			return err
		}
		defer executor.Close()
		for _, grant := range grants {
			access := "read-only files"
			if grant.Writable {
				access = "file edits"
			}
			fmt.Fprintf(o.stderr, "Sharing %s: %s and shell commands enabled. Commands can access the internet, localhost services and your local network. Ctrl-C stops sharing.\n", grant.ID, access)
		}
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
			if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 409 && apiErr.Code != "device_busy") {
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
	connect.Flags().StringArrayVar(&writeFolders, "write-folder", nil, "Writable folder ALIAS=/absolute/path with file edits and sandboxed shell commands (repeatable)")
	connect.Flags().StringArrayVar(&blocked, "block", nil, "Blocked relative path on every shared folder (repeatable)")
	connect.Flags().StringArrayVar(&readOnly, "read-only", nil, "Read-only relative path on every writable folder (repeatable)")
	connect.Flags().StringVar(&stateDir, "state-dir", "", "Private receipt storage outside all shared folders")
	connect.Flags().BoolVar(&downloads, "downloads", false, "Also allow reading and writing ~/Downloads from each shared project (explicit opt-in)")
	root.AddCommand(connect)
	return root
}

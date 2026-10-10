package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/cobra"
)

// executorParams is one local connection: the folders to share and how they are guarded.
type executorParams struct {
	deviceID, stateDir                       string
	folders, writeFolders, blocked, readOnly []string
	downloads                                bool
	// connected runs each time the connection is (re)established; trace sees every served request (debugging).
	connected func()
	// waiting runs when the server refuses this connection because the computer already holds one (a second folder shared while the
	// first is still connected), with the reason to show, so `agentworks start` can say so instead of only timing out.
	waiting func(reason string)
	trace   func(request localfiles.Request, response localfiles.Response, took time.Duration)
}

func runExecutor(ctx context.Context, o *options, p executorParams) error {
	if !localfiles.ValidID(p.deviceID) {
		return errors.New("--device requires a stable identifier (letters, numbers, underscores or hyphens)")
	}
	config, err := o.path()
	if err != nil {
		return err
	}
	stateDir := p.stateDir
	if stateDir == "" {
		stateDir = filepath.Join(filepath.Dir(config), "executor-state", p.deviceID)
	}
	var grants []localfiles.Grant
	for _, group := range []struct {
		values   []string
		writable bool
	}{{p.folders, false}, {p.writeFolders, true}} {
		for _, value := range group.values {
			alias, folder, ok := strings.Cut(value, "=")
			if !ok || !localfiles.ValidID(alias) || !filepath.IsAbs(folder) {
				return errors.New("folders must be ALIAS=/absolute/path")
			}
			guard := wf.FolderGuard{ReadPaths: []string{"."}, BlockedPaths: p.blocked, ReadOnlyPaths: p.readOnly}
			if group.writable {
				guard.WritePaths = []string{"."}
			}
			grants = append(grants, localfiles.Grant{Resource: localfiles.Resource{ID: alias, Writable: group.writable, Shell: true, Guard: guard}, Root: folder, State: filepath.Join(stateDir, alias), PrivatePaths: []string{config}})
		}
	}
	if p.downloads {
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
		grants = append(grants, localfiles.Grant{Resource: localfiles.Resource{ID: "downloads", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: p.blocked, ReadOnlyPaths: p.readOnly}}, Root: filepath.Join(home, "Downloads"), State: filepath.Join(stateDir, "downloads"), PrivatePaths: []string{config}})
	}
	if len(grants) > 0 {
		if err := configureLocalShellSandbox(); err != nil {
			return err
		}
	}
	executor, err := localfiles.Open(p.deviceID, grants)
	if err != nil {
		return err
	}
	defer executor.Close()
	executor.Hello.CLIVersion = cliVersion
	executor.Hello.CLIBuild = cliBuild
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
	if p.trace != nil {
		client.WithExecutorTrace(p.trace)
	}
	delay := time.Second
	for {
		err = client.ServeExecutor(ctx, executor, func() {
			delay = time.Second
			fmt.Fprintf(o.stderr, "Executor %s connected with %d folder(s). Ctrl-C disconnects.\n", p.deviceID, len(grants))
			if p.connected != nil {
				p.connected()
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		var apiErr *agentworksclient.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 409 && apiErr.Code != "device_busy") {
			return err
		}
		if p.waiting != nil && errors.As(err, &apiErr) && apiErr.Code == "device_busy" {
			p.waiting("another folder on this computer is already being shared, and a computer shares one folder at a time")
		}
		// Exponential backoff (1s, 2s, 4s ... 30s) plus up to a quarter of the delay at random, so many computers that lost the
		// server at the same moment do not all retry in the same second.
		wait := delay + time.Duration(rand.Int64N(int64(delay/4)+1))
		fmt.Fprintf(o.stderr, "Executor disconnected; reconnecting in %s.\n", wait.Round(100*time.Millisecond))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}

func executorCommand(o *options) *cobra.Command {
	var p executorParams
	root := &cobra.Command{Use: "executor", Short: "Share explicitly selected local folders with server agents (advanced; most people use `agentworks start`)"}
	connect := &cobra.Command{Use: "connect", Args: cobra.NoArgs, Short: "Keep an outbound authenticated file and shell connection open; Ctrl-C disconnects", RunE: func(cmd *cobra.Command, _ []string) error {
		return runExecutor(cmd.Context(), o, p)
	}}
	connect.Flags().StringVar(&p.deviceID, "device", "", "Stable device ID, e.g. work-laptop")
	connect.Flags().StringArrayVar(&p.folders, "folder", nil, "Read-only folder ALIAS=/absolute/path (repeatable)")
	connect.Flags().StringArrayVar(&p.writeFolders, "write-folder", nil, "Writable folder ALIAS=/absolute/path with file edits and sandboxed shell commands (repeatable)")
	connect.Flags().StringArrayVar(&p.blocked, "block", nil, "Blocked relative path on every shared folder (repeatable)")
	connect.Flags().StringArrayVar(&p.readOnly, "read-only", nil, "Read-only relative path on every writable folder (repeatable)")
	connect.Flags().StringVar(&p.stateDir, "state-dir", "", "Private receipt storage outside all shared folders")
	connect.Flags().BoolVar(&p.downloads, "downloads", false, "Also allow reading and writing ~/Downloads from each shared project (explicit opt-in)")
	root.AddCommand(connect)
	return root
}

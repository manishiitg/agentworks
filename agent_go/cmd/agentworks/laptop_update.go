package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Refresh only computer connections that already exist. Folder and terminal
// grants stay on the server; an update never starts a new device or a sign-in.
func refreshLaptopConnections(ctx context.Context, o *options, executable, build string) error {
	dirs := map[string]bool{}
	if path, err := o.path(); err == nil {
		dirs[filepath.Join(filepath.Dir(path), "shares")] = true
	}
	logins, _ := agentworksclient.ListLogins()
	for _, login := range logins {
		if path, err := agentworksclient.LoginPath(login.Name); err == nil {
			dirs[filepath.Join(filepath.Dir(path), "shares")] = true
		}
	}
	var problems []error
	for dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			return err
		}
		for _, statePath := range files {
			st, ok := readShareState(statePath)
			if !ok || st.PID <= 0 || !processAlive(st.PID) || build != "" && st.CLIBuild == build {
				continue
			}
			if err := refreshLaptopConnection(ctx, o, executable, dir, statePath, st); err != nil {
				problems = append(problems, fmt.Errorf("computer %s: %w", st.Device, err))
			}
		}
	}
	return errors.Join(problems...)
}

func refreshLaptopConnection(ctx context.Context, o *options, executable, dir, statePath string, st shareState) error {
	if st.ConfigPath == "" || st.OwnerID == "" {
		return errors.New("this connection predates saved account identity; reconnect the computer once with agentworks stop and agentworks start; saved Crew folders and permissions stay unchanged")
	}
	key := strings.TrimSuffix(filepath.Base(statePath), ".json")
	if !filepath.IsAbs(st.ConfigPath) || filepath.Clean(filepath.Dir(st.ConfigPath)) != filepath.Clean(filepath.Dir(dir)) || shareKey(st.ConfigPath) != key {
		return errors.New("the saved computer connection does not match its login; it was left running")
	}
	cfg, err := agentworksclient.LoadConfig(st.ConfigPath)
	if err != nil {
		return err
	}
	if cfg.Server != st.Server {
		return errors.New("the saved login changed server; the computer connection was left running")
	}
	connection := *o
	connection.configPath, connection.serverURL = st.ConfigPath, st.Server
	client, err := connection.client()
	if err != nil {
		return err
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = client.WithExecutorOwner(st.OwnerID).CheckExecutorOwner(check)
	cancel()
	if err != nil {
		return err // Account refusal or unreachable server: do not stop the existing process.
	}
	current, ok := readShareState(statePath)
	if !ok || current.PID != st.PID || !processAlive(st.PID) {
		return nil
	}
	if st.ProcessStart == "" || processStartID(st.PID) != st.ProcessStart {
		return errors.New("the recorded process has ended or changed; no process was stopped")
	}
	fmt.Fprintf(o.stderr, "Reconnecting computer %s with the updated AgentWorks CLI...\n", st.Device)
	terminateProcess(st.PID, false)
	for i := 0; i < 25 && processAlive(st.PID); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(st.PID) {
		terminateProcess(st.PID, true)
	}
	_ = os.Remove(statePath)
	wait, done := context.WithTimeout(ctx, 40*time.Second)
	defer done()
	return runShareBackground(wait, &connection, startFlags{device: st.Device, ownerID: st.OwnerID, executable: executable}, "", st.Device, "", "", st.Server, dir, key, statePath, false, func() error {
		return errors.New("the saved sign-in needs renewal; sign in to this computer connection's account")
	})
}
